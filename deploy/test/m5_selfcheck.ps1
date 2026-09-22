# M5 一键自检 —— 演示/联调前一条命令确认全链路正常.
#
# 检查五件事(对应 2026-10-01 计划 Day 5 主线):
#   A 三服务健康      GET /health -> status + 各组件(mysql/redis)都 ok
#   B gRPC 真实探活   leasing gRPC Ping(不是"端口开着就算过")
#   C 四路聚合降级    GET /api/dashboard/overview -> 200 + 未起的上游如实列在 degraded 里
#   D WebSocket      无 token 必须 401; 带自签 access token 必须升级成功并收到 snapshot
#   E 关键接口       租赁/调度 4 个列表接口 200
#
# 前置: Docker 在跑、mysql/redis 容器健康(脚本会自己检查并给出结论).
# 用法: powershell -ExecutionPolicy Bypass -File .\deploy\test\m5_selfcheck.ps1
# 退出码: 0 = 全部通过; 1 = 有失败项.
#
# 端口刻意错开(18051~18053 / 19051), 避免与你自己 go run 起来的开发实例(8051~8053 / 9051)抢端口。

param(
    [int]   $LeasePort    = 18051,
    [int]   $DashPort     = 18052,
    [int]   $DispatchPort = 18053,
    [int]   $GrpcPort     = 19051,
    [string]$JwtSecret    = "m5-selfcheck-secret",
    [int]   $StartTimeoutSec = 90,
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
$TempDir  = Join-Path $env:TEMP "m5-selfcheck"
New-Item -ItemType Directory -Force -Path $TempDir | Out-Null

# ---------- HTTP 小工具 ----------
function Get-Json($url, $headers) {
    try {
        if ($headers) { return Invoke-RestMethod -Uri $url -Headers $headers -TimeoutSec 15 }
        return Invoke-RestMethod -Uri $url -TimeoutSec 15
    } catch { return $null }
}
function Get-Status($url, $headers) {
    try {
        if ($headers) { $r = Invoke-WebRequest -Uri $url -Headers $headers -UseBasicParsing -TimeoutSec 15 }
        else          { $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 15 }
        return [int]$r.StatusCode
    } catch {
        # 非 2xx 会抛异常, 状态码在响应里 —— WS 的 401 检查依赖这一点
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        return -1
    }
}
# 自签一个 access token(HS256), 与 common/jwt.Generate 同格式: user_id/role_ids/tenant_id/type/exp
function New-AccessToken($secret, [int]$expireSec = 3600) {
    $b64 = {
        param($s)
        [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($s)).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    }
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $header = & $b64 '{"alg":"HS256","typ":"JWT"}'
    $payload = & $b64 (@{
        user_id   = 1
        role_ids  = "1"
        tenant_id = 0
        type      = "access"
        exp       = $now + $expireSec
        iat       = $now
    } | ConvertTo-Json -Compress)
    $hmac = New-Object System.Security.Cryptography.HMACSHA256
    $hmac.Key = [Text.Encoding]::UTF8.GetBytes($secret)
    # 签名是对 base64url 后的 header.payload 直接做 HMAC, 结果再做 base64url(不能先转成字符串再过一遍 b64)
    $sigRaw = [Convert]::ToBase64String($hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes("$header.$payload")))
    $sig = $sigRaw.TrimEnd('=').Replace('+', '-').Replace('/', '_')
    return "$header.$payload.$sig"
}

$Procs = @()
Push-Location $RepoRoot
try {
    # ================= 0. 前置 =================
    Step "0. 前置检查"
    docker ps --format '{{.Names}} {{.Status}}' 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) { Bad "Docker 未运行 —— 先启动 Docker Desktop"; exit 1 }
    $mw = docker ps --format '{{.Names}} {{.Status}}'
    foreach ($c in @("onepark-mysql", "onepark-redis")) {
        Assert-True ([bool]($mw | Select-String -Pattern "$c.*healthy")) "$c 健康"
    }
    & go build ./app/leasing-service/... ./app/dashboard-service/... ./app/dispatch-service/... 2>&1 | Out-Null
    Assert-True ($LASTEXITCODE -eq 0) "go build 通过"

    # ================= 1. 临时配置 =================
    Step "1. 生成临时配置(只放 TEMP, 不改仓库 etc/)"
    function New-TempCfg($srcRel, $dstName, $find, $replaceWith) {
        $src = Join-Path $RepoRoot $srcRel
        $cfg = Get-Content $src -Raw
        $new = $cfg -replace [regex]::Escape($find), $replaceWith
        # 替换必须"真的发生", 否则静默用着原端口 -> 与开发实例抢端口, 现象很难定位。
        # (HTTP 用 Port: 8051; gRPC 用 ListenOn: 0.0.0.0:9051 —— RpcServerConf 的字段名不同, 这里踩过一次。)
        if ($new -eq $cfg) { Bad "未能替换 $srcRel 中的 [$find] —— 配置模板可能已变"; return $null }
        $dst = Join-Path $TempDir $dstName
        [System.IO.File]::WriteAllText($dst, $new, (New-Object System.Text.UTF8Encoding($false)))
        Ok "$dstName : $find -> $replaceWith"
        return $dst
    }
    $cfgLease    = New-TempCfg "app\leasing-service\etc\leasing-api.yaml"      "leasing-api.yaml"   "Port: 8051"              "Port: $LeasePort"
    $cfgLeaseGrpc= New-TempCfg "app\leasing-service\etc\leasing-grpc.yaml"     "leasing-grpc.yaml"  "ListenOn: 0.0.0.0:9051"  "ListenOn: 0.0.0.0:$GrpcPort"
    $cfgDash     = New-TempCfg "app\dashboard-service\etc\dashboard-api.yaml"  "dashboard-api.yaml" "Port: 8052"              "Port: $DashPort"
    $cfgDispatch = New-TempCfg "app\dispatch-service\etc\dispatch-api.yaml"    "dispatch-api.yaml"  "Port: 8053"              "Port: $DispatchPort"
    if (-not ($cfgLease -and $cfgLeaseGrpc -and $cfgDash -and $cfgDispatch)) { Bad "配置生成失败, 中止"; exit 1 }

    # JWT 密钥通过环境变量注入(三个配置都是 json:",env=JWT_SECRET"):
    # 自检要验证"带合法 token 的 WS 能接入", 就必须自己知道密钥 —— 用临时密钥, 不碰任何真实密钥。
    $env:JWT_SECRET = $JwtSecret

    # ================= 2. 起服务 =================
    Step "2. 起 4 个进程(leasing HTTP/gRPC, dashboard, dispatch)"
    $logs = @{}
    function Start-Svc($name, $mainPkg, $cfg, $port) {
        $log = Join-Path $TempDir "$name.log"
        $logs[$name] = $log
        $p = Start-Process -FilePath "go" -ArgumentList @("run", $mainPkg, "-f", $cfg) `
                -NoNewWindow -PassThru -RedirectStandardOutput $log -RedirectStandardError "$log.err"
        $script:Procs += $p
        for ($i = 0; $i -lt $StartTimeoutSec; $i++) {
            Start-Sleep -Seconds 1
            if (Test-NetConnection -ComputerName 127.0.0.1 -Port $port -InformationLevel Quiet -WarningAction SilentlyContinue) {
                Ok "$name 就绪(端口 $port, 等待 ${i}s)"
                return $true
            }
            if ($p.HasExited) { Bad "$name 启动失败(退出码 $($p.ExitCode)), 日志: $log"; return $false }
        }
        Bad "$name 在 ${StartTimeoutSec}s 内未就绪, 日志: $log"
        return $false
    }
    $null = Start-Svc "leasing-api"    "./app/leasing-service"              $cfgLease     $LeasePort
    $null = Start-Svc "leasing-grpc"   "./app/leasing-service/grpcserver"   $cfgLeaseGrpc $GrpcPort
    $null = Start-Svc "dashboard-api"  "./app/dashboard-service"            $cfgDash      $DashPort
    $null = Start-Svc "dispatch-api"   "./app/dispatch-service"             $cfgDispatch  $DispatchPort

    # ================= A. 健康 =================
    Step "A. 三服务健康"
    foreach ($svc in @(@("leasing", $LeasePort), @("dashboard", $DashPort), @("dispatch", $DispatchPort))) {
        $name = $svc[0]; $port = $svc[1]
        $h = Get-Json "http://127.0.0.1:$port/health"
        if (-not $h) { Bad "$name /health 无响应"; continue }
        Assert-True ($h.status -eq "ok") "$name /health status=$($h.status)(组件: $(($h.components | ForEach-Object { "$($_.name)=$($_.ok)" }) -join ', '))"
        $bad = @($h.components | Where-Object { -not $_.ok })
        Assert-True ($bad.Count -eq 0) "$name 依赖组件全部 ok"
    }

    # ================= B. gRPC 真实探活 =================
    Step "B. leasing gRPC 真实 RPC 探活"
    $pingOut = & go run ./app/leasing-service/tools/grpcping -addr "127.0.0.1:$GrpcPort" 2>&1
    $pingOk = ($LASTEXITCODE -eq 0)
    Assert-True $pingOk "gRPC Ping 成功: $($pingOut | Out-String | ForEach-Object { $_.Trim() })"
    Info "注意: 只探测 TCP 端口是不够的 —— 端口开着不代表 RPC 可用, 这里走的是真调用"

    # ================= C. 四路聚合与降级语义 =================
    Step "C. 大屏聚合: 逐源降级语义"
    $ov = Get-Json "http://127.0.0.1:$DashPort/api/dashboard/overview"
    if (-not $ov) { Bad "overview 无响应" } else {
        Ok "overview 返回 200(上游未起也必须返回, 这是 NonBlock 设计)"
        $degraded = @($ov.degraded)
        Info "degraded = [$($degraded -join ', ')]"
        Assert-True ($degraded.Count -gt 0) "未启动的上游被如实列进 degraded(而不是悄悄给 0)"
        foreach ($card in @("alarm", "device", "work_order", "energy")) {
            if ($degraded -contains $card) {
                Assert-True ($null -eq $ov.$card) "降级卡片 $card 是 null 而不是 0(前端据此标'暂不可用')"
            }
        }
        Assert-True ($ov.cached -ne $null) "响应带 cached 标记(便于判断是否命中缓存)"
    }

    # ================= D. WebSocket 鉴权 + 快照 =================
    Step "D. WebSocket: 无 token 拒绝 / 有 token 接入并收快照"
    $wsUrl = "ws://127.0.0.1:$DashPort/ws/dashboard"

    # 刻意**手写 HTTP 升级握手**而不是用 System.Net.WebSockets.ClientWebSocket:
    # 后者在本机 .NET Framework 上对"带 query 的 ws:// 地址"会抛 "not supported for a relative URI"
    # (无 token 那种不带 query 的地址反而正常) —— 那是客户端库的怪癖, 与被测服务无关。
    # 自检要的是确定性: 裸 TCP 发一次标准握手, 只看 HTTP 状态码与随后的一帧, 不受框架影响。
    function Test-WsHandshake([string]$uri, [int]$timeoutMs = 7000) {
        $u = [Uri]$uri
        $client = New-Object System.Net.Sockets.TcpClient
        try {
            $client.Connect($u.Host, $u.Port)
            $stream = $client.GetStream()
            $stream.ReadTimeout = $timeoutMs
            # RFC6455 要求的握手头(Key 任意随机 base64, 自检不需要校验 Sec-WebSocket-Accept)
            $req = "GET $($u.PathAndQuery) HTTP/1.1`r`n" +
                   "Host: $($u.Host):$($u.Port)`r`n" +
                   "Upgrade: websocket`r`nConnection: Upgrade`r`n" +
                   "Sec-WebSocket-Key: $([Convert]::ToBase64String([Guid]::NewGuid().ToByteArray()))`r`n" +
                   "Sec-WebSocket-Version: 13`r`n`r`n"
            $bytes = [Text.Encoding]::ASCII.GetBytes($req)
            $stream.Write($bytes, 0, $bytes.Length); $stream.Flush()

            # 读满整个响应头(可能分多次到达)
            $sb = New-Object System.Text.StringBuilder
            $buf = New-Object byte[] 4096
            for ($i = 0; $i -lt 5; $i++) {
                $n = $stream.Read($buf, 0, $buf.Length)
                if ($n -le 0) { break }
                [void]$sb.Append([Text.Encoding]::UTF8.GetString($buf, 0, $n))
                if ($sb.ToString() -match "`r`n`r`n") { break }
            }
            $head = $sb.ToString()
            $statusLine = ($head -split "`r`n")[0].Trim()

            $frame = ""
            if ($statusLine -match " 101") {
                # 快照推送间隔 5s; 这里再读一帧, 帧头 0x81 + 长度 + JSON
                $n2 = $stream.Read($buf, 0, $buf.Length)
                if ($n2 -gt 0) { $frame = [Text.Encoding]::UTF8.GetString($buf, 0, $n2) }
            }
            return @{ code = $statusLine; frame = $frame; err = "" }
        } catch {
            return @{ code = ""; frame = ""; err = $_.Exception.Message }
        } finally { $client.Close() }
    }

    $noToken = Test-WsHandshake $wsUrl
    Assert-True ($noToken.code -match "401") "无 token 被拒绝(实际: $($noToken.code)$($noToken.err))"

    $token = New-AccessToken $JwtSecret
    # 先验 token 形状(三段 base64url): 形状不对时报错直接指向"签名/编码", 不必去猜服务端
    Assert-True ($token -match '^[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+$') `
        "自签 token 形状正确(长度 $($token.Length), 三段 base64url)"
    Info "连接地址: $wsUrl`?token=<长度 $($token.Length)>"

    $withToken = Test-WsHandshake "$wsUrl`?token=$token"
    if (-not ($withToken.code -match " 101")) {
        Bad "带合法 token 未升级成功(实际: $($withToken.code)$($withToken.err))"
    } else {
        Ok "带合法 access token 完成 101 升级"
        Assert-True ($withToken.frame -match "snapshot") `
            "收到快照帧(前 120 字符: $($withToken.frame.Substring(0, [Math]::Min(120, $withToken.frame.Length))))"
    }

    # ================= E. 关键接口 =================
    Step "E. 关键接口 200"
    $apis = @(
        @("租赁-园区列表", "http://127.0.0.1:$LeasePort/api/lease/zones"),
        @("租赁-合同列表", "http://127.0.0.1:$LeasePort/api/lease/contracts"),
        @("调度-人员列表", "http://127.0.0.1:$DispatchPort/api/dispatch/staffs"),
        @("调度-工单列表", "http://127.0.0.1:$DispatchPort/api/dispatches")
    )
    foreach ($a in $apis) {
        $code = Get-Status $a[1]
        Assert-True ($code -eq 200) "$($a[0]) -> $code"
    }

}
finally {
    Pop-Location
    if (-not $KeepRunning) {
        Step "清理"
        foreach ($p in $Procs) { if ($p -and -not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue } }
        # go run 会派生子进程, 按端口兜底
        foreach ($port in @($LeasePort, $DashPort, $DispatchPort, $GrpcPort)) {
            Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue |
                ForEach-Object { Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue }
        }
        Info "进程已停止; 日志留在 $TempDir(加 -KeepRunning 可保留进程)"
    } else {
        Info "-KeepRunning: 进程保留, 端口 $LeasePort/$DashPort/$DispatchPort/$GrpcPort"
    }
}

# ================= 汇总 =================
Write-Host "`n================ 自检结果 ================" -ForegroundColor Cyan
Write-Host ("PASS={0}  FAIL={1}" -f $script:Passed, $script:Failed) -ForegroundColor $(if ($script:Failed -eq 0) { "Green" } else { "Red" })
if ($script:Failed -eq 0) {
    Write-Host "M5 全链路自检通过 ✅ 可以开始演示/联调" -ForegroundColor Green
    exit 0
}
Write-Host "存在失败项, 请按上方 [FAIL] 逐条排查; 服务日志: $TempDir" -ForegroundColor Red
exit 1
