# 前端联调用: 启动「招商租赁 / 能耗分析 / 用户管理」三个后端服务.
#
# 背景: 各服务 etc/*.yaml 的 DSN 由环境变量注入, deploy/.env 里的主机名是容器网络
# (mysql / redis), 本地原生 go run 直接连会解析不到, 因此脚本统一覆盖为 127.0.0.1.
#
# 用法:
#   powershell -File scripts/dev-up-frontend.ps1
# 停止:
#   Get-Process go | Stop-Process -Force
# 日志:
#   logs/<service>.log / logs/<service>.err.log

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$logDir = Join-Path $root 'logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null

# ---- 读取 deploy/.env ----
$envMap = @{}
foreach ($line in Get-Content (Join-Path $root 'deploy/.env') -Encoding UTF8) {
    if ($line -match '^\s*#' -or [string]::IsNullOrWhiteSpace($line)) { continue }
    $idx = $line.IndexOf('=')
    if ($idx -lt 1) { continue }
    $envMap[$line.Substring(0, $idx).Trim()] = $line.Substring($idx + 1).Trim()
}

$pwdMysql = $envMap['MYSQL_ROOT_PASSWORD']
# 端口说明: 本机 3306/6379 已被另一套项目的容器(xl-order-mysql / xl-order-redis)占用,
# OnePark 自己的容器映射在 13306 / 16379(docker ps 可见 onepark-mysql / onepark-redis).
# 若你的环境端口不同, 改下面三个值即可.
$mysqlPort = '13306'
$redisAddr = '127.0.0.1:16379'

$envMap['MYSQL_HOST'] = '127.0.0.1'
$envMap['MYSQL_PORT'] = $mysqlPort
$envMap['MYSQL_DATABASE'] = 'sys_db'          # user-manage 使用 sys_db(RBAC 五表)
$envMap['REDIS_ADDR'] = $redisAddr            # 覆盖 .env 的容器主机名 redis:6379
$envMap['LEASING_MYSQL_DSN'] = "root:$pwdMysql@tcp(127.0.0.1:$mysqlPort)/leasing_db?charset=utf8mb4&parseTime=True&loc=Local"
# energy_reading 实际建在 billing_db(见 deploy/sql/m2_energy_reading.sql): 由 energy-data-service
# 写入、billing-service 计费读取. energy-analysis-service 是同一份数据的只读方, 因此这里指向
# billing_db 而不是空的 energy_analysis_db —— 否则日报/月报会报 table doesn't exist.
$envMap['ENERGY_ANALYSIS_MYSQL_DSN'] = "root:$pwdMysql@tcp(127.0.0.1:$mysqlPort)/billing_db?charset=utf8mb4&parseTime=True&loc=Local"
$envMap['USER_GRPC_ADDR'] = '127.0.0.1:18089' # 网关 RBAC 用的 gRPC(可选)

foreach ($k in $envMap.Keys) {
    Set-Item -Path "Env:$k" -Value $envMap[$k]
}

# ---- 启动服务 ----
$services = @(
    @{ Name = 'user-manage';      Main = '.\app\user-manage\usermanage.go';                       Cfg = '.\app\user-manage\etc\usermanage-api.yaml';                       Port = 8086 },
    @{ Name = 'leasing';          Main = '.\app\leasing-service\leasing.go';                      Cfg = '.\app\leasing-service\etc\leasing-api.yaml';                      Port = 8051 },
    @{ Name = 'energy-analysis';  Main = '.\app\energy-analysis-service\energyanalysis.go';       Cfg = '.\app\energy-analysis-service\etc\energyanalysis-api.yaml';       Port = 8062 }
)

foreach ($s in $services) {
    $existing = Get-NetTCPConnection -State Listen -LocalPort $s.Port -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Host "[skip] $($s.Name) 端口 $($s.Port) 已在监听" -ForegroundColor Yellow
        continue
    }
    Start-Process -FilePath 'go' `
        -ArgumentList 'run', $s.Main, '-f', $s.Cfg `
        -WorkingDirectory $root `
        -RedirectStandardOutput (Join-Path $logDir "$($s.Name).log") `
        -RedirectStandardError (Join-Path $logDir "$($s.Name).err.log") `
        -WindowStyle Hidden
    Write-Host "[start] $($s.Name) -> $($s.Port)"
}

Write-Host '等待编译启动(go run 首次编译较慢, 约 30~60s)...' -ForegroundColor Cyan
Start-Sleep -Seconds 25
foreach ($s in $services) {
    $ok = Get-NetTCPConnection -State Listen -LocalPort $s.Port -ErrorAction SilentlyContinue
    if ($ok) {
        Write-Host "[ok]   $($s.Name) 已监听 $($s.Port)" -ForegroundColor Green
    }
    else {
        Write-Host "[warn] $($s.Name) 未就绪, 查看 logs/$($s.Name).err.log" -ForegroundColor Red
    }
}
