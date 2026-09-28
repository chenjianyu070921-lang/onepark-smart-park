# M5 演示数据 seed —— 演示前 30 秒把数据铺好.
#
# 为什么需要它: 演示最怕「数据是空的, 看不出效果」。这条脚本让大屏/看板一开就有内容。
#
# 铺什么(全是 M5 自己的数据, 走真实 HTTP 接口, 因此校验/审计/状态机全都真实生效):
#   - 园区      : 2 个演示园区(入驻率的分母)
#   - 合同      : 6 份(生效中 / 即将到期 / 已到期 / 待生效), 入驻率与到期提醒都有数
#   - 账单      : 调 POST /bill/auto 生成上期账单, 再挑几张缴费 -> 已缴/未缴/欠费都有
#   - 调度人员  : 6 名(不同技能/区域/在岗状态), 让自动派单**有得选**才有看点
#   - 调度工单  : 5 张(待指派/已指派/处理中/已完成), 看板与状态流转都有料
#
# ⚠️ 范围声明(如实说): 本脚本只铺 **M5 自己的数据**。
#    大屏四张卡片(告警/设备/工单/能耗)的数据源是 **M1~M4 的 gRPC 服务**, M5 无权也不该替它们造数据 ——
#    那四张卡片要有数, 需要 M1~M4 各自服务起着(或它们的 seed)。
#
# 幂等: 默认先清掉自己上次铺的演示数据(按 DEMO 标记), 再重新铺 —— 每次跑完都是**同一个确定状态**。
#       加 -NoReset 则跳过清理(已有数据时直接补)。
#
# 用法: powershell -ExecutionPolicy Bypass -File .\deploy\test\m5_seed.ps1
# 退出码: 0 = 铺设并校验通过; 1 = 有失败项。

param(
    [int]   $LeasePort    = 18051,
    [int]   $DashPort     = 18052,
    [int]   $DispatchPort = 18053,
    [int]   $GrpcPort     = 19051,
    [string]$JwtSecret    = "m5-seed-secret",
    # 演示数据落在哪个园区。⚠️ 必须是**非 0**: 写接口校验 x-tenant-id 时把 0 当成"未注入"直接 400
    # (`M6-E-0001 缺少租户信息`) —— 平台口径「默认园区 = 1」, 这里跟它对齐。
    [long]  $TenantId     = 1,
    [switch]$NoReset,
    [switch]$KeepRunning
)

$ErrorActionPreference = "Stop"
$script:Passed = 0
$script:Failed = 0

function Ok($m)   { Write-Host "  [ OK ] $m" -ForegroundColor Green;  $script:Passed++ }
function Bad($m)  { Write-Host "  [FAIL] $m" -ForegroundColor Red;    $script:Failed++ }
function Step($m) { Write-Host "`n=== $m ===" -ForegroundColor Cyan }
function Info($m) { Write-Host "  [info] $m" -ForegroundColor DarkGray }
function Assert-True($c, $what) { if ($c) { Ok $what } else { Bad $what } }

$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$TempDir  = Join-Path $env:TEMP "m5-seed"
New-Item -ItemType Directory -Force -Path $TempDir | Out-Null

# 演示数据统一标记 —— 清理与校验都靠它, 保证"只碰自己的数据"
$DemoZoneA = "DEMO-A-1F"
$DemoZoneB = "DEMO-B-2F"
$DemoTenantName = "DEMO-演示租户"
$StaffIdFrom = 900001
$StaffIdTo   = 900099

# ---------- 基础工具 ----------
# 服务配置里的 ${VAR} 占位符**必须真的被设置**, 否则行为很糟(见下):
#   ① 缺 *_MYSQL_DSN / REDIS_* -> 服务能起但连不上库, 请求全报错;
#   ② 缺 DEVICE_RPC_ENDPOINTS 等四个 -> dashboard **启动即 fatal**
#      (zrpc.MustNewClient 拿到空地址直接 Must 退出), 而不是"连不上就降级"。
# 这里从 deploy/.env 装载(与 compose 同一份来源), 再补上本地直连用的四路上游地址。
function Import-DotEnv($path) {
    if (-not (Test-Path $path)) { return }
    foreach ($line in Get-Content -Path $path -Encoding UTF8) {
        $t = $line.Trim()
        if (-not $t -or $t.StartsWith('#') -or $t -notmatch '=') { continue }
        $kv = $t.Split('=', 2)
        if ($kv[0] -and -not (Get-Item "env:$($kv[0])" -ErrorAction SilentlyContinue)) {
            Set-Item -Path "env:$($kv[0])" -Value $kv[1]
        }
    }
}
Import-DotEnv (Join-Path $RepoRoot "deploy\.env")
# 四路上游: deploy/.env 里没有(它的模板给的是容器名), 本地直跑要用 127.0.0.1 + 各自 etc 里的端口
# (device 9001 / workorder 9091 / alarm 9009 / energy-data 9061)
foreach ($kv in @{
        DEVICE_RPC_ENDPOINTS      = "127.0.0.1:9001"
        WORKORDER_RPC_ENDPOINTS   = "127.0.0.1:9091"
        ALARM_RPC_ENDPOINTS       = "127.0.0.1:9009"
        ENERGY_DATA_RPC_ENDPOINTS = "127.0.0.1:9061"
    }.GetEnumerator()) {
    if (-not (Get-Item "env:$($kv.Key)" -ErrorAction SilentlyContinue)) {
        Set-Item -Path "env:$($kv.Key)" -Value $kv.Value
    }
}

function Get-MysqlPass {
    $envFile = Join-Path $RepoRoot "deploy\.env"
    if (Test-Path $envFile) {
        $t = Get-Content $envFile -Raw
        if ($t -match 'MYSQL_ROOT_PASSWORD=(\S+)') { return $Matches[1] }
    }
    return "onepark123"
}
$MysqlPass = Get-MysqlPass
function Sql($q) {
    $out = docker exec -e "MYSQL_PWD=$MysqlPass" onepark-mysql mysql -N -B -u root $script:DbName -e $q
    return ($out | Out-String).Trim()
}
function Scalar($q) {
    $r = Sql $q
    if (-not $r) { return "" }
    return ($r -split "`n")[0].Trim()
}
function Api($method, $path, $body, $port = 0) {
    if ($port -eq 0) { $port = $LeasePort }
    $uri = "http://127.0.0.1:$port$path"
    # ⚠️ 两个都必须处理, 否则写入接口会以各种方式失败:
    #  ① 鉴权/租户已收口: 写接口要求 x-tenant-id, 缺了直接 400 `M6-E-0001 缺少租户信息`
    #     (只读接口不强制, 所以"读能通、写全挂"很有迷惑性);
    #  ② PS 5.1 传字符串 body 时**不按 UTF-8 编码** -> 中文租户名/人名会变成乱码落库,
    #     必须自己转成 UTF-8 字节再发。
    $headers = @{ "x-tenant-id" = "$TenantId" }
    try {
        if ($null -ne $body) {
            $json = $body | ConvertTo-Json -Compress
            $bytes = [System.Text.Encoding]::UTF8.GetBytes($json)
            $r = Invoke-RestMethod -Method $method -Uri $uri -Headers $headers -Body $bytes `
                    -ContentType "application/json; charset=utf-8" -TimeoutSec 20
        } else {
            $r = Invoke-RestMethod -Method $method -Uri $uri -Headers $headers -TimeoutSec 20
        }
        if ($null -ne $r.data) { return $r.data }
        return $r
    } catch {
        # 必须把响应体带出来: go-zero 的 4xx 会把**具体原因**写在 body 的 msg 里
        # ("400 Bad Request" 这一句本身不提供任何线索, 排障时等于没有信息)。
        $ex = $_.Exception
        $detail = ""
        if ($_.ErrorDetails -and $_.ErrorDetails.Message) { $detail = $_.ErrorDetails.Message }
        elseif ($ex.Response) {
            try {
                $sr = New-Object System.IO.StreamReader($ex.Response.GetResponseStream())
                $detail = $sr.ReadToEnd()
            } catch { }
        }
        while ($ex.InnerException) { $ex = $ex.InnerException }
        Bad "HTTP $method $uri 失败: $($ex.Message) | 响应体: $detail"
        return $null
    }
}
function Add-Days([int]$n) { return (Get-Date).AddDays($n).ToString("yyyy-MM-dd") }

$Procs = @()
Push-Location $RepoRoot
try {
    # ================= 0. 前置 =================
    Step "0. 前置检查"
    docker ps --format '{{.Names}} {{.Status}}' 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) { Bad "Docker 未运行"; exit 1 }
    $mw = docker ps --format '{{.Names}} {{.Status}}'
    foreach ($c in @("onepark-mysql", "onepark-redis")) {
        Assert-True ([bool]($mw | Select-String -Pattern "$c.*healthy")) "$c 健康"
    }
    & go build ./app/leasing-service/... ./app/dashboard-service/... ./app/dispatch-service/... 2>&1 | Out-Null
    Assert-True ($LASTEXITCODE -eq 0) "go build 通过"

    # ================= 1. 临时配置 + 起服务 =================
    Step "1. 起服务(临时配置, 端口错开, 不改仓库 etc/)"
    function New-TempCfg($srcRel, $dstName, $find, $replaceWith) {
        $cfg = Get-Content (Join-Path $RepoRoot $srcRel) -Raw
        $new = $cfg -replace [regex]::Escape($find), $replaceWith
        if ($new -eq $cfg) { Bad "未能替换 $srcRel 中的 [$find]"; return $null }
        $dst = Join-Path $TempDir $dstName
        [System.IO.File]::WriteAllText($dst, $new, (New-Object System.Text.UTF8Encoding($false)))
        return $dst
    }
    $cfgLease    = New-TempCfg "app\leasing-service\etc\leasing-api.yaml"     "leasing-api.yaml"   "Port: 8051"             "Port: $LeasePort"
    $cfgLeaseGrpc= New-TempCfg "app\leasing-service\etc\leasing-grpc.yaml"    "leasing-grpc.yaml"  "ListenOn: 0.0.0.0:9051" "ListenOn: 0.0.0.0:$GrpcPort"
    $cfgDash     = New-TempCfg "app\dashboard-service\etc\dashboard-api.yaml" "dashboard-api.yaml" "Port: 8052"             "Port: $DashPort"
    $cfgDispatch = New-TempCfg "app\dispatch-service\etc\dispatch-api.yaml"   "dispatch-api.yaml"  "Port: 8053"             "Port: $DispatchPort"
    if (-not ($cfgLease -and $cfgLeaseGrpc -and $cfgDash -and $cfgDispatch)) { Bad "配置生成失败"; exit 1 }
    Ok "4 份临时配置就绪"

    $env:JWT_SECRET = $JwtSecret
    function Start-Svc($name, $pkg, $cfg, $port) {
        $log = Join-Path $TempDir "$name.log"
        $p = Start-Process -FilePath "go" -ArgumentList @("run", $pkg, "-f", $cfg) `
                -NoNewWindow -PassThru -RedirectStandardOutput $log -RedirectStandardError "$log.err"
        $script:Procs += $p
        for ($i = 0; $i -lt 90; $i++) {
            Start-Sleep -Seconds 1
            if (Test-NetConnection -ComputerName 127.0.0.1 -Port $port -InformationLevel Quiet -WarningAction SilentlyContinue) {
                return $true
            }
            if ($p.HasExited) { Bad "$name 启动失败, 日志: $log"; return $false }
        }
        Bad "$name 未就绪(90s), 日志: $log"
        return $false
    }
    $svcOk = $true
    $svcOk = (Start-Svc "leasing-api" "./app/leasing-service" $cfgLease $LeasePort) -and $svcOk
    $svcOk = (Start-Svc "leasing-grpc" "./app/leasing-service/grpcserver" $cfgLeaseGrpc $GrpcPort) -and $svcOk
    $svcOk = (Start-Svc "dashboard-api" "./app/dashboard-service" $cfgDash $DashPort) -and $svcOk
    $svcOk = (Start-Svc "dispatch-api" "./app/dispatch-service" $cfgDispatch $DispatchPort) -and $svcOk
    Assert-True $svcOk "4 个进程就绪"
    if (-not $svcOk) { exit 1 }

    # ================= 2. 清掉上次的演示数据 =================
    Step "2. 清理上次演示数据(按 DEMO 标记, 只碰自己的)"
    if ($NoReset) {
        Info "-NoReset: 跳过清理"
    } else {
        $script:DbName = "dispatch_db"
        Sql "DELETE l FROM dispatch_task_log l JOIN dispatch_task t ON t.id = l.task_id WHERE t.title LIKE 'DEMO-%'" | Out-Null
        Sql "DELETE FROM dispatch_task WHERE title LIKE 'DEMO-%'" | Out-Null
        Sql "DELETE FROM dispatch_staff WHERE staff_id BETWEEN $StaffIdFrom AND $StaffIdTo" | Out-Null

        $script:DbName = "leasing_db"
        Sql "DELETE b FROM lease_bill b JOIN lease_contract c ON c.id = b.contract_id WHERE c.tenant_name = '$DemoTenantName'" | Out-Null
        Sql "DELETE l FROM lease_contract_status_log l JOIN lease_contract c ON c.id = l.contract_id WHERE c.tenant_name = '$DemoTenantName'" | Out-Null
        Sql "DELETE FROM lease_contract WHERE tenant_name = '$DemoTenantName'" | Out-Null
        Sql "DELETE FROM lease_zone WHERE zone_code IN ('$DemoZoneA','$DemoZoneB')" | Out-Null
        Ok "上次演示数据已清理(合同/账单/审计/园区/人员/工单)"
    }

    # ================= 3. 园区 =================
    Step "3. 铺园区(入驻率的分母)"
    $script:DbName = "leasing_db"
    $zones = @(
        @{ zone_code = $DemoZoneA; zone_name = "演示 A 座 1 层"; total_area_sqm = 6000.0 },
        @{ zone_code = $DemoZoneB; zone_name = "演示 B 座 2 层"; total_area_sqm = 4500.0 }
    )
    foreach ($z in $zones) {
        $r = Api "POST" "/api/lease/zone" $z
        Assert-True ($null -ne $r -and $r.id -gt 0) "园区 $($z.zone_code) upsert 成功(id=$($r.id))"
    }

    # ================= 4. 合同 =================
    Step "4. 铺合同(生效中 / 即将到期 / 已到期 / 待生效)"
    # 每份合同: 月租 / 面积 / 起止 / 是否自动续约 / 说明
    $contracts = @(
        @{ tag = "ACT-1"; rent = "38000.00"; area = 620; from = -300; to = 400; renew = 1; note = "生效中(长期)" },
        @{ tag = "ACT-2"; rent = "26000.00"; area = 410; from = -200; to = 20;  renew = 1; note = "生效中(即将到期, 触发到期提醒)" },
        @{ tag = "ACT-3"; rent = "15000.00"; area = 260; from = -90;  to = 275; renew = 0; note = "生效中(不自动续约)" },
        @{ tag = "EXP-1"; rent = "8000.00";  area = 150; from = -400; to = -10; renew = 0; note = "已过终止日 -> 待每日维护转已到期" },
        @{ tag = "NEW-1"; rent = "12000.00"; area = 200; from = 10;   to = 380; renew = 1; note = "待生效(未来起租)" },
        @{ tag = "ACT-4"; rent = "22000.00"; area = 330; from = -400; to = -5;  renew = 1; note = "已过终止日 + 自动续约 -> 待续签" }
    )
    $createdIds = @{}
    $i = 0
    foreach ($c in $contracts) {
        $i++
        $body = @{
            tenant_id       = 0
            tenant_name     = $DemoTenantName
            zone_code       = $DemoZoneA
            area_sqm        = $c.area
            monthly_rent    = $c.rent
            deposit         = $c.rent
            start_date      = (Add-Days $c.from)
            end_date        = (Add-Days $c.to)
            auto_renew      = $c.renew
            renew_notice_days = 30
        }
        $r = Api "POST" "/api/lease/contract" $body
        if ($null -eq $r -or -not $r.id) { Bad "合同 $($c.tag) 创建失败"; continue }
        $createdIds[$c.tag] = $r.id
        # 建出来是「待生效」; 除 NEW-1 外都推进入「生效中」, 这样入驻率与出账才有量。
        # ⚠️ 合同的状态变更走 `PUT /api/lease/contract/:id` 带 action
        #    (不是 `/contract/:id/status` —— 那是账单与工单的写法, 合同这边会 404)。
        if ($c.tag -ne "NEW-1") {
            $null = Api "PUT" "/api/lease/contract/$($r.id)" @{ action = "activate"; remark = "seed-$($c.tag)" }
        }
        Ok "合同 $($c.tag)($($c.note)) 已建 id=$($r.id)"
    }
    Assert-True ($createdIds.Count -eq 6) "6 份合同创建成功"

    # ================= 5. 账单(用真实出账接口) =================
    Step "5. 铺账单: 调 /bill/auto 出上期账, 再挑几张缴费"
    $auto = Api "POST" "/api/lease/bill/auto" @{ remark = "seed" }
    Assert-True ($null -ne $auto) "出账接口调用成功: $($auto | ConvertTo-Json -Compress)"

    Start-Sleep -Seconds 1
    # 本租户的账单(按合同归属筛), 前两张标记已缴 -> 汇总里 已收/欠费 都有数
    $demoContractIds = ($createdIds.Values | Where-Object { $_ }) -join ","
    $payIds = @()
    if ($demoContractIds) {
        $raw = Sql "SELECT id FROM lease_bill WHERE contract_id IN ($demoContractIds) ORDER BY id LIMIT 2"
        if ($raw) { $payIds = @($raw -split "`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ }) }
    }
    foreach ($id in $payIds) {
        $r = Api "PUT" "/api/lease/bill/$id/status" @{ action = "pay"; remark = "seed-演示缴费" }
        Assert-True ($null -ne $r) "账单 $id 标记已缴"
    }
    if ($payIds.Count -eq 0) { Info "未找到可缴费账单(可能上期无合同, 属正常)" }

    # ================= 6. 调度人员 =================
    Step "6. 铺调度人员(不同技能/区域/在岗, 让派单有得选)"
    $staff = @(
        @{ id = 900001; name = "演示-消防班长";  zone = $DemoZoneA; skills = "fire,security";      on = 1 },
        @{ id = 900002; name = "演示-强电技师";  zone = $DemoZoneA; skills = "electrical";         on = 1 },
        @{ id = 900003; name = "演示-安防员";    zone = $DemoZoneB; skills = "security";           on = 1 },
        @{ id = 900004; name = "演示-综合维修";  zone = $DemoZoneB; skills = "electrical,fire";    on = 1 },
        @{ id = 900005; name = "演示-备勤(不在岗)"; zone = $DemoZoneA; skills = "fire,electrical"; on = 0 },
        @{ id = 900006; name = "演示-夜间巡查";  zone = $DemoZoneB; skills = "security,fire";      on = 1 }
    )
    foreach ($s in $staff) {
        $r = Api "POST" "/api/dispatch/staff" @{
            staff_id = $s.id; name = $s.name; phone = "13800000000"
            zone_code = $s.zone; skills = $s.skills; on_duty = $s.on; status = 1
        } $DispatchPort
        Assert-True ($null -ne $r) "人员 $($s.name) upsert 成功"
    }
    $scan = Api "GET" "/api/dispatch/staffs" $null $DispatchPort
    $staffTotal = 0
    if ($scan -and $scan.list) { $staffTotal = @($scan.list).Count }
    Assert-True ($staffTotal -ge 6) "人员池已铺开(当前共 $staffTotal 人, 含历史数据)"

    # ================= 7. 调度工单 =================
    Step "7. 铺调度工单(覆盖待指派/已指派/处理中/已完成)"
    $tasks = @(
        @{ title = "DEMO-消防通道堵塞"; zone = $DemoZoneA; skill = "fire";       pri = 1 },
        @{ title = "DEMO-配电箱异响";   zone = $DemoZoneA; skill = "electrical"; pri = 2 },
        @{ title = "DEMO-门禁读卡故障"; zone = $DemoZoneB; skill = "security";   pri = 2 },
        @{ title = "DEMO-楼层照明检修"; zone = $DemoZoneB; skill = "electrical"; pri = 3 },
        @{ title = "DEMO-消防栓月度巡检"; zone = $DemoZoneA; skill = "fire";     pri = 3 }
    )
    $taskIds = @()
    foreach ($t in $tasks) {
        $r = Api "POST" "/api/dispatch" @{
            title = $t.title; source = 1; zone_code = $t.zone
            required_skill = $t.skill; priority = $t.pri; description = "演示数据"
        } $DispatchPort
        if ($null -eq $r -or -not $r.id) { Bad "工单 $($t.title) 创建失败"; continue }
        $taskIds += [int]$r.id
        Ok "工单已建 id=$($r.id) $($t.title)"
    }
    # 让看板有不同状态: 第1张保持待指派; 第2张自动派单; 第3张 start; 第4张 start+finish
    # ⚠️ 端口必须显式传 $DispatchPort: Api 的默认端口是租赁服务的, 漏传会打到租赁上 -> 404
    if ($taskIds.Count -ge 2) {
        $a = Api "PUT" "/api/dispatch/$($taskIds[1])/assign" @{ assignee_id = 0 } $DispatchPort
        if ($a -and $a.assignee_id -gt 0) { Ok "工单 $($taskIds[1]) 已自动派单给 $($a.assignee_name)" }
        else { Info "工单 $($taskIds[1]) 自动派单未成功(可能无在岗人员)" }
    }
    if ($taskIds.Count -ge 3) {
        $null = Api "PUT" "/api/dispatch/$($taskIds[2])/assign" @{ assignee_id = 0 } $DispatchPort
        $r = Api "PUT" "/api/dispatch/$($taskIds[2])/status" @{ action = "start"; remark = "seed" } $DispatchPort
        Assert-True ($null -ne $r) "工单 $($taskIds[2]) 已推进到「处理中」"
    }
    if ($taskIds.Count -ge 4) {
        $null = Api "PUT" "/api/dispatch/$($taskIds[3])/assign" @{ assignee_id = 0 } $DispatchPort
        $null = Api "PUT" "/api/dispatch/$($taskIds[3])/status" @{ action = "start"; remark = "seed" } $DispatchPort
        $r = Api "PUT" "/api/dispatch/$($taskIds[3])/status" @{ action = "finish"; remark = "seed" } $DispatchPort
        Assert-True ($null -ne $r) "工单 $($taskIds[3]) 已推进到「已完成」"
    }

    # ================= 8. 校验(读接口回读) =================
    Step "8. 回读校验(演示前确认看板有数)"
    $occ = Api "GET" "/api/lease/occupancy" $null
    Assert-True ($null -ne $occ) "入驻率有响应: $($occ | ConvertTo-Json -Compress)"

    $sum = Api "GET" "/api/lease/bills/summary" $null
    if ($sum) {
        Ok "账单汇总: $($sum | ConvertTo-Json -Compress)"
        Assert-True ([double]$sum.unpaid_amount -ge 0) "欠费金额已计算"
    } else { Bad "账单汇总无响应" }

    $exp = Api "GET" "/api/lease/contracts/expiring" $null
    $expN = 0; if ($exp) { if ($exp.list) { $expN = @($exp.list).Count } elseif ($exp -is [array]) { $expN = $exp.Count } }
    Assert-True ($expN -ge 1) "到期提醒有数据($expN 份将到期)"

    $dis = Api "GET" "/api/dispatches?page=1&page_size=20" $null $DispatchPort
    if ($dis) {
        $dn = 0; if ($dis.list) { $dn = @($dis.list).Count }
        Assert-True ($dn -ge 5) "工单看板有数据(前 20 条内有 $dn 条)"
    } else { Bad "工单列表无响应" }

    $ov = Api "GET" "/api/dashboard/overview" $null $DashPort
    if ($ov) {
        Ok "大屏 overview 正常返回(degraded=[$(@($ov.degraded) -join ', ')])"
        Info "注意: 大屏四张卡片的数据源是 M1~M4 的 gRPC, M5 无权替它们造数据 —— 那四张要有数需 M1~M4 起着"
    } else { Bad "大屏 overview 无响应" }

}
finally {
    Pop-Location
    if (-not $KeepRunning) {
        Step "清理进程"
        foreach ($p in $Procs) { if ($p -and -not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue } }
        foreach ($port in @($LeasePort, $DashPort, $DispatchPort, $GrpcPort)) {
            Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue |
                ForEach-Object { Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue }
        }
        Info "进程已停止; 演示数据已留在库里(加 -NoReset 可增量补, 或重跑本脚本获得确定状态)"
    } else {
        Info "-KeepRunning: 进程保留(端口 $LeasePort/$DashPort/$DispatchPort/$GrpcPort), 可直接开演示"
    }
}

Write-Host "`n================ seed 结果 ================" -ForegroundColor Cyan
Write-Host ("PASS={0}  FAIL={1}" -f $script:Passed, $script:Failed) -ForegroundColor $(if ($script:Failed -eq 0) { "Green" } else { "Red" })
if ($script:Failed -eq 0) {
    Write-Host "演示数据已铺好 ✅ 可以直接开始演示" -ForegroundColor Green
    exit 0
}
Write-Host "存在失败项, 请按上方 [FAIL] 排查; 服务日志: $TempDir" -ForegroundColor Red
exit 1
