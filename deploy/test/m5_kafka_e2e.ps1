# M5 Kafka 端到端一键验证脚本.
#
# 一次执行完成五件事(对应 2026-10-01 计划 Day 1):
#   主线  投告警 -> 消费者自动建单(含审计), 端到端打通
#   加练A 完整业务闭环: 建单 -> 自动派单 -> 超时重派 -> 达上限释放回池 -> 人工指派 -> start/finish/close
#   加练B 双实例同消费组并发投递, 断言只落 1 单(uk_alarm_id 幂等)
#   加练C 毒消息(非法 JSON / 缺 request_id)不阻塞分区, 后续正常消息仍被处理
#   收尾  清理本次产生的数据与临时文件, 输出断言汇总
#
# 前置: 本地 broker 已起(见 deploy/kafka/README.md), mysql/redis 容器在跑.
# 用法: powershell -ExecutionPolicy Bypass -File .\deploy\test\m5_kafka_e2e.ps1
# 退出码: 0 = 全部断言通过; 1 = 有断言失败或前置不满足.
#
# ⚠️ 未执行验证声明: 本脚本在编写时**未能实跑**(本机拉不下 kafka 镜像, 见 deploy/kafka/README.md),
#    断言口径均来自代码常量(action/status/source 的取值已逐一核对), 但首次执行仍可能因环境差异需微调。
#    跑通后请把结果回填 docs/m5/06-Kafka端到端验证报告.md。

param(
    [string]$Brokers       = "127.0.0.1:19092",
    [string]$Topic         = "alarm-event",
    [string]$Group         = "",
    [int]   $HttpPort      = 18053,
    [int]   $HttpPort2     = 18054,
    [int]   $ReassignSec   = 5,
    [string]$MysqlContainer = "onepark-mysql",
    [string]$RedisContainer = "onepark-redis",
    [string]$Db            = "dispatch_db"
)

$ErrorActionPreference = "Stop"
$script:Passed = 0
$script:Failed = 0

function Ok($m)   { Write-Host "  [ OK ] $m" -ForegroundColor Green;  $script:Passed++ }
function Bad($m)  { Write-Host "  [FAIL] $m" -ForegroundColor Red;    $script:Failed++ }
function Step($m) { Write-Host "`n=== $m ===" -ForegroundColor Cyan }
function Info($m) { Write-Host "  [info] $m" -ForegroundColor DarkGray }

function Assert-Eq($actual, $expected, $what) {
    if ("$actual" -eq "$expected") { Ok "$what = $actual" } else { Bad "$what = [$actual], 期望 [$expected]" }
}
function Assert-True($cond, $what) {
    if ($cond) { Ok $what } else { Bad $what }
}
# 写无 BOM 的 UTF-8 文件(PS 5.1 的 Set-Content -Encoding UTF8 会带 BOM, 会污染 yaml)
function Write-Utf8NoBom($path, $text) {
    [System.IO.File]::WriteAllText($path, $text, (New-Object System.Text.UTF8Encoding($false)))
}

# 读服务日志。两个必须处理的现实:
#   1. go-zero 的 logx 走 **stderr** —— 一部分日志在 *.log.err 里, 只读 *.log 会漏(踩过一次);
#   2. 服务进程还活着时文件被占用, Get-Content 会静默失败返回空 —— 用 ReadAllText + 重试。
function Read-SvcLogs {
    $sb = New-Object System.Text.StringBuilder
    foreach ($f in @($Log1, "$Log1.err", $Log2, "$Log2.err")) {
        if (-not (Test-Path $f)) { continue }
        # 必须用 FileShare.ReadWrite 打开: 服务进程还在写, 独占式读取会失败
        try {
            $fs = New-Object System.IO.FileStream($f, [System.IO.FileMode]::Open,
                    [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
            $sr = New-Object System.IO.StreamReader($fs, [System.Text.Encoding]::UTF8)
            [void]$sb.Append($sr.ReadToEnd())
            $sr.Close(); $fs.Close()
        } catch { }
    }
    return $sb.ToString()
}
# 日志断言带轮询: 日志落盘比 DB 稍慢, 且服务仍在运行, 一次读不到不代表没有
function Assert-Log($pattern, $what, [int]$timeoutSec = 20) {
    for ($i = 0; $i -lt $timeoutSec; $i++) {
        if ((Read-SvcLogs) -match $pattern) { Ok "$what (等待 ${i}s)"; return }
        Start-Sleep -Seconds 1
    }
    Bad "$what —— ${timeoutSec}s 内日志里未出现该内容"
}

# ---------- 路径与凭据 ----------
$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$EnvFile  = Join-Path $RepoRoot "deploy\.env"
$MysqlPass = "onepark123"; $RedisPass = "onepark123"
if (Test-Path $EnvFile) {
    $env_text = Get-Content $EnvFile -Raw
    if ($env_text -match 'MYSQL_ROOT_PASSWORD=(\S+)') { $MysqlPass = $Matches[1] }
    if ($env_text -match 'REDIS_PASSWORD=(\S+)')      { $RedisPass = $Matches[1] }
}
$TempCfg  = Join-Path $env:TEMP "dispatch-api.kafka-e2e.yaml"
$LogDir   = Join-Path $env:TEMP "m5-kafka-e2e"
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
$Log1 = Join-Path $LogDir "svc1.log"
$Log2 = Join-Path $LogDir "svc2.log"

if (-not $Group) { $Group = "m5-dispatch-e2e-local" }   # 固定组名: 每次跑前会清位移, 便于复跑

# ---------- 小工具 ----------
function Sql($q) {
    # ⚠️ 密码走 MYSQL_PWD 环境变量, 不用 -p: 命令行带密码会往 stderr 打警告,
    #    而 $ErrorActionPreference='Stop' 会把原生命令的 stderr 当成**终止错误**直接中断脚本
    #    (2>$null 挡不住这条规则)。同理 redis 侧用 REDISCLI_AUTH。
    $out = docker exec -e "MYSQL_PWD=$MysqlPass" $MysqlContainer mysql -N -B -u root $Db -e $q
    return ($out | Out-String).Trim()
}
function Scalar($q) {
    $r = Sql $q
    if (-not $r) { return "" }
    return ($r -split "`n")[0].Trim()
}
function Api($method, $path, $body) {
    $uri = "http://127.0.0.1:$HttpPort$path"
    try {
        if ($null -ne $body) {
            $r = Invoke-RestMethod -Method $method -Uri $uri -Body ($body | ConvertTo-Json -Compress) `
                    -ContentType "application/json" -TimeoutSec 15
        } else {
            $r = Invoke-RestMethod -Method $method -Uri $uri -TimeoutSec 15
        }
        # go-zero 默认包一层 {code,msg,data}; 兼容不包的情况
        if ($null -ne $r.data) { return $r.data }
        return $r
    } catch {
        Bad "HTTP $method $path 失败: $($_.Exception.Message)"
        return $null
    }
}
function Wait-Until([scriptblock]$cond, [int]$timeoutSec, [string]$what) {
    for ($i = 0; $i -lt $timeoutSec; $i++) {
        if (& $cond) { Ok "$what (等待 ${i}s)" ; return $true }
        Start-Sleep -Seconds 1
    }
    Bad "$what —— 等待 ${timeoutSec}s 仍未满足"
    return $false
}

# ================= 0. 前置检查 =================
Step "0. 前置检查"
Info "repo=$RepoRoot  broker=$Brokers  topic=$Topic  group=$Group  port=$HttpPort"

$tcp = Test-NetConnection -ComputerName ($Brokers -split ':')[0] -Port ([int]($Brokers -split ':')[1]) `
        -InformationLevel Quiet -WarningAction SilentlyContinue
if (-not $tcp) {
    Bad "broker $Brokers 连不上 —— 先按 deploy/kafka/README.md 起本地 broker"
    Write-Host "`n汇总: PASS=$script:Passed FAIL=$script:Failed" -ForegroundColor Yellow
    exit 1
}
Ok "broker 可连: $Brokers"

if (-not (docker ps --format '{{.Names}}' | Select-String -Quiet $MysqlContainer)) {
    Bad "mysql 容器 $MysqlContainer 未运行"; exit 1
}
Ok "mysql 容器在跑: $MysqlContainer"

Push-Location $RepoRoot
try {
    & go build ./app/dispatch-service/... 2>&1 | Out-Null
    Assert-Eq $LASTEXITCODE 0 "go build 通过"

    # 清掉可能残留的重派锁 —— 它加锁后靠 TTL 过期, 上一次异常退出会挡住本次扫描(表现为 ReassignCount 一直是 0)
    docker exec -e "REDISCLI_AUTH=$RedisPass" $RedisContainer redis-cli DEL "m5:dispatch:lock:reassign" | Out-Null

    # ================= 1. 生成临时配置 =================
    Step "1. 生成临时配置(不改仓库里的 etc/, 只放在 TEMP)"
    $cfg = Get-Content "app\dispatch-service\etc\dispatch-api.yaml" -Raw

    # 每项替换都断言"确实替换到了" —— 配置模板变了要立刻发现, 而不是静默用着旧值跑出一堆迷惑断言
    $before = $cfg
    $cfg = $cfg -replace 'Port: 8053', "Port: $HttpPort"
    Assert-True ($cfg -ne $before) "替换 HTTP 端口 -> $HttpPort"

    $before = $cfg
    $cfg = $cfg -replace 'IntervalSec: 60', "IntervalSec: $ReassignSec"
    Assert-True ($cfg -ne $before) "重派周期压到 ${ReassignSec}s(便于验证重派/释放)"

    $before = $cfg
    $cfg = $cfg -replace 'Brokers: "[^"]*"', "Brokers: `"$Brokers`""
    Assert-True ($cfg -ne $before) "Kafka.Brokers -> $Brokers"

    $before = $cfg
    $cfg = $cfg -replace 'Group: "[^"]*"', "Group: `"$Group`""
    Assert-True ($cfg -ne $before) "Kafka.Group -> $Group"

    # ⚠️ 兜底园区必须与查询口径一致: 消费者按 DefaultTenantId 落库, 而详情/流转按请求租户过滤。
    #    两者不一致会表现为"工单建出来了但查不到"(配置注释里专门警告过这一点)。
    #    开发库的存量工单是 tenant 0, 故这里统一用 0; 网关注入非 0 租户时需同步调整。
    $before = $cfg
    $cfg = $cfg -replace 'DefaultTenantId: \d+', "DefaultTenantId: 0"
    Assert-True ($cfg -ne $before) "Kafka.DefaultTenantId -> 0(与开发库存量口径一致)"

    $before = $cfg
    $cfg = $cfg -replace '(?m)^(\s*)Enabled: false', '$1Enabled: true'
    Assert-True ($cfg -ne $before) "Kafka.Enabled -> true"

    # 刻意不用 Set-Content -Encoding UTF8: PS 5.1 会写入 BOM, 而 go-zero 的 yaml 解析器对 BOM 不一定友好。
    # 用无 BOM 的 UTF-8 写出, 与仓库里其它 yaml 保持一致。
    Write-Utf8NoBom $TempCfg $cfg
    Ok "临时配置已写入 $TempCfg"

    # ================= 2. 起 2 个服务实例 =================
    Step "2. 起服务实例(消费组相同, 用于主线与并发验证)"
    $p1 = Start-Process -FilePath "go" -ArgumentList @("run", "./app/dispatch-service", "-f", $TempCfg) `
            -NoNewWindow -PassThru -RedirectStandardOutput $Log1 -RedirectStandardError "$Log1.err"
    Info "实例1 PID=$($p1.Id) 日志=$Log1"

    Wait-Until { Test-NetConnection -ComputerName 127.0.0.1 -Port $HttpPort -InformationLevel Quiet -WarningAction SilentlyContinue } 90 "实例1 端口就绪" | Out-Null
    Start-Sleep -Seconds 3   # 等消费循环真正起起来(端口先于消费者就绪)

    $health = Api "GET" "/health" $null
    Assert-True ($null -ne $health) "/health 有响应"

    # ================= 3. 主线 =================
    Step "3. 主线: 投告警 -> 自动建单 + 审计"
    $rid = "req-e2e-main-$([DateTimeOffset]::Now.ToUnixTimeMilliseconds())"
    $zone = "A-1F-101"
    $out = & go run ./app/dispatch-service/tools/alarmproducer -brokers $Brokers -topic $Topic `
            -event-type fire -device-id "SMOKE-E2E-01" -zone $zone -request-id $rid 2>&1
    Info ($out | Out-String).Trim()
    Assert-True ($LASTEXITCODE -eq 0) "告警投递成功(request_id=$rid)"

    $taskId = ""
    Wait-Until {
        $script:taskId = Scalar "SELECT id FROM dispatch_task WHERE alarm_id='$rid' LIMIT 1"
        return [bool]$script:taskId
    } 30 "消费者自动建单" | Out-Null

    if ($taskId) {
        Assert-Eq (Scalar "SELECT COUNT(*) FROM dispatch_task WHERE alarm_id='$rid'") 1 "工单条数(uk_alarm_id 幂等)"
        Assert-Eq (Scalar "SELECT status FROM dispatch_task WHERE id=$taskId") 1 "初始状态 = 待指派"
        Assert-Eq (Scalar "SELECT source FROM dispatch_task WHERE id=$taskId") 2 "来源 = 告警自动创建"
        Assert-Eq (Scalar "SELECT zone_code FROM dispatch_task WHERE id=$taskId") $zone "区域取到了 payload.zone_code"
        Assert-Eq (Scalar "SELECT COUNT(*) FROM dispatch_task_log WHERE task_id=$taskId AND action='create'") 1 "审计: 1 条 create"
        Assert-Eq (Scalar "SELECT CONCAT(from_status,'->',to_status) FROM dispatch_task_log WHERE task_id=$taskId AND action='create'") "0->1" "审计: 0 -> 1"
    }

    # ================= 4. 加练 A: 完整业务闭环 =================
    Step "4. 加练 A: 建单 -> 派单 -> 重派 -> 释放 -> 人工 -> start/finish/close"
    if ($taskId) {
        $assign = Api "PUT" "/api/dispatch/$taskId/assign" @{ assignee_id = 0 }
        if ($assign -and $assign.assignee_id -and [int]$assign.assignee_id -ne 0) {
            Ok "自动派单成功: assignee_id=$($assign.assignee_id) name=$($assign.assignee_name)"
            Assert-Eq (Scalar "SELECT status FROM dispatch_task WHERE id=$taskId") 2 "派单后状态 = 已指派"

            # 超时重派: 把超时窗口改到过去, 等 cron 扫一次
            Sql "UPDATE dispatch_task SET assign_expire_at = NOW() - INTERVAL 1 MINUTE WHERE id=$taskId" | Out-Null
            Wait-Until { [int](Scalar "SELECT reassign_count FROM dispatch_task WHERE id=$taskId") -ge 1 } ($ReassignSec + 20) "cron 超时重派(ReassignCount>=1)" | Out-Null
            Assert-True ([int](Scalar "SELECT COUNT(*) FROM dispatch_task_log WHERE task_id=$taskId AND action='assign'") -ge 2) "审计: assign 至少 2 条(首次派单 + 重派)"

            # 达上限释放: 直接把次数顶到上限(临时配置里 MaxReassign 保持默认 3)再触发一次扫描
            Sql "UPDATE dispatch_task SET reassign_count = 3, assign_expire_at = NOW() - INTERVAL 1 MINUTE WHERE id=$taskId" | Out-Null
            Wait-Until { [int](Scalar "SELECT status FROM dispatch_task WHERE id=$taskId") -eq 1 } ($ReassignSec + 20) "达重派上限后释放回待指派" | Out-Null
            Assert-True ([int](Scalar "SELECT COUNT(*) FROM dispatch_task_log WHERE task_id=$taskId AND action='release'") -ge 1) "审计: 有 release 记录"
        } else {
            Bad "自动派单未返回处理人 —— 需先有在岗人员(检查 dispatch_staff), 后续人工指派仍会继续验证"
        }

        # 人工指派 -> start -> finish -> close
        # 在岗条件与 assign.Pool 的筛选保持一致: on_duty=1(在岗) AND status=1(启用)
        $staff = Scalar "SELECT id FROM dispatch_staff WHERE on_duty=1 AND status=1 ORDER BY id LIMIT 1"
        if ($staff) {
            $manual = Api "PUT" "/api/dispatch/$taskId/assign" @{ assignee_id = [int]$staff }
            Assert-True ($manual -and [int]$manual.assignee_id -eq [int]$staff) "人工指派给 staff=$staff"
        } else {
            Info "库中无在岗人员, 跳人工指派(该分支已由单测覆盖)"
        }

        foreach ($act in @("start", "finish", "close")) {
            $r = Api "PUT" "/api/dispatch/$taskId/status" @{ action = $act; remark = "e2e-$act" }
            Assert-True ($null -ne $r) "状态流转 $act 成功"
        }
        Assert-Eq (Scalar "SELECT status FROM dispatch_task WHERE id=$taskId") 5 "终态 = 已关闭"
        Assert-True ([int](Scalar "SELECT COUNT(*) FROM dispatch_task_log WHERE task_id=$taskId") -ge 6) "审计流水贯穿全程(>=6 条)"
        Assert-True ((Scalar "SELECT finished_at FROM dispatch_task WHERE id=$taskId") -ne "NULL") "完成时间已写入"
        Info "审计动作序列: $((Sql "SELECT GROUP_CONCAT(action ORDER BY id) FROM dispatch_task_log WHERE task_id=$taskId"))"
    }

    # ================= 5. 加练 B: 双实例并发 =================
    Step "5. 加练 B: 起第二个实例(同消费组), 同一告警并发投 2 条"
    # 实例2 必须换 HTTP 端口(否则与实例1 抢端口), 但**消费组必须相同** —— 这正是并发消费的前提
    $TempCfg2 = Join-Path $env:TEMP "dispatch-api.kafka-e2e-2.yaml"
    $cfg2 = (Get-Content $TempCfg -Raw) -replace "Port: $HttpPort", "Port: $HttpPort2"
    Write-Utf8NoBom $TempCfg2 $cfg2
    Assert-True ((Get-Content $TempCfg2 -Raw) -match "Port: $HttpPort2") "实例2 配置端口 -> $HttpPort2"
    $p2 = Start-Process -FilePath "go" -ArgumentList @("run", "./app/dispatch-service", "-f", $TempCfg2) `
            -NoNewWindow -PassThru -RedirectStandardOutput $Log2 -RedirectStandardError "$Log2.err"
    Info "实例2 PID=$($p2.Id) 端口=$HttpPort2(消费组同实例1)"
    Wait-Until { Test-NetConnection -ComputerName 127.0.0.1 -Port $HttpPort2 -InformationLevel Quiet -WarningAction SilentlyContinue } 90 "实例2 端口就绪" | Out-Null
    Start-Sleep -Seconds 3

    $ridB = "req-e2e-dup-$([DateTimeOffset]::Now.ToUnixTimeMilliseconds())"
    # 同一个 request_id 连投 2 条: 两实例并行消费, 必然有一方撞唯一键 -> 幂等跳过
    & go run ./app/dispatch-service/tools/alarmproducer -brokers $Brokers -topic $Topic `
        -event-type intrusion -device-id "DOOR-E2E-01" -zone $zone -request-id $ridB -count 2 2>&1 | Out-Null
    Assert-True ($LASTEXITCODE -eq 0) "同 id 并发投递 2 条"

    $dupId = ""
    Wait-Until {
        $script:dupId = Scalar "SELECT id FROM dispatch_task WHERE alarm_id='$ridB' LIMIT 1"
        return [bool]$script:dupId
    } 30 "并发场景建单完成" | Out-Null
    Start-Sleep -Seconds 5   # 给"晚到的那条"留出撞唯一键并跳过的时间
    if ($dupId) {
        Assert-Eq (Scalar "SELECT COUNT(*) FROM dispatch_task WHERE alarm_id='$ridB'") 1 "并发下只落 1 张工单"
        Assert-Eq (Scalar "SELECT COUNT(*) FROM dispatch_task_log WHERE task_id=$dupId AND action='create'") 1 "并发下审计只有 1 条 create"
    }
    # 日志断言统一放在第 7 步停服务之后 —— 进程运行时会占住重定向文件, 中途读不到(踩过两次)
    Info "幂等日志断言见第 7 步"

    # ================= 6. 加练 C: 毒消息不阻塞分区 =================
    Step "6. 加练 C: 毒消息(非法 JSON / 缺 request_id) 不阻塞分区"
    $ridC = "req-e2e-poison-$([DateTimeOffset]::Now.ToUnixTimeMilliseconds())"
    $producer = "./app/dispatch-service/tools/alarmproducer"
    & go run $producer -brokers $Brokers -topic $Topic -raw "{ this is not json" 2>&1 | Out-Null
    & go run $producer -brokers $Brokers -topic $Topic -raw '{"device_id":"NO-REQ-ID"}' 2>&1 | Out-Null
    & go run $producer -brokers $Brokers -topic $Topic -event-type fault -device-id "POISON-AFTER-01" -zone $zone -request-id $ridC 2>&1 | Out-Null

    $afterId = ""
    Wait-Until {
        $script:afterId = Scalar "SELECT id FROM dispatch_task WHERE alarm_id='$ridC' LIMIT 1"
        return [bool]$script:afterId
    } 45 "毒消息之后的正常消息**仍被处理**" | Out-Null
    Assert-True ([bool]$afterId) "分区未被毒消息卡死(关键断言)"

    Assert-Log "丢弃非法告警消息" "坏消息被记录并跳过(日志有'丢弃非法告警消息')"

    # ================= 7. 清理 =================
    Step "7. 清理"
    foreach ($p in @($p1, $p2)) {
        if ($p -and -not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
    }
    # go run 会派生子进程, 按端口兜底清理
    foreach ($port in @($HttpPort, $HttpPort2)) {
        Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue |
            ForEach-Object { Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue }
    }
    # 停完进程再读日志: 此时文件句柄已释放, 读取才可靠
    Assert-Log "幂等跳过" "加练B 日志: 出现幂等跳过(证明真撞了唯一键, 而非只有一条被消费)"
    Assert-Log "丢弃非法告警消息" "加练C 日志: 坏消息被记录并跳过"

    Sql "DELETE FROM dispatch_task_log WHERE task_id IN (SELECT id FROM dispatch_task WHERE alarm_id LIKE 'req-e2e-%')" | Out-Null
    Sql "DELETE FROM dispatch_task WHERE alarm_id LIKE 'req-e2e-%'" | Out-Null
    docker exec -e "REDISCLI_AUTH=$RedisPass" $RedisContainer redis-cli DEL "m5:dispatch:lock:reassign" | Out-Null
    Remove-Item $TempCfg, $TempCfg2 -ErrorAction SilentlyContinue
    $left = Scalar "SELECT COUNT(*) FROM dispatch_task WHERE alarm_id LIKE 'req-e2e-%'"
    Assert-Eq $left 0 "测试数据已清理(残留 0 行)"
    Info "服务日志保留在 $LogDir 供排障"

}
finally {
    Pop-Location
}

# ================= 汇总 =================
Write-Host "`n================ 汇总 ================" -ForegroundColor Cyan
Write-Host ("PASS={0}  FAIL={1}" -f $script:Passed, $script:Failed) -ForegroundColor $(if ($script:Failed -eq 0) { "Green" } else { "Red" })
if ($script:Failed -eq 0) {
    Write-Host "全部断言通过 ✅ 可回填 docs/m5/07-Kafka端到端验证报告.md" -ForegroundColor Green
    exit 0
}
Write-Host "存在失败断言, 请按上方 [FAIL] 逐条排查; 服务日志: $LogDir" -ForegroundColor Red
exit 1
