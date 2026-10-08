<#
  start-local-m2.ps1 — OnePark M2 物业模块本地启动(优化版)
  ---------------------------------------------------------------
  优化点(相对此前每次 `go run` 4 个服务的启动方式):
    1. 编译一次: 仅当 bin/<svc>.exe 缺失或比 app/<svc>-service/go.mod 旧时才 `go build`,
       增量编译通常 3~6s; 日常重启不再重复编译, 启动更快。
    2. 跑预编译二进制: 不再用 `go run`(其会常驻一个 go 编译器父进程 + 临时二进制,
       4 个服务即 8 个进程、额外占数百 MB 内存)。改为直接运行 bin/<svc>.exe, 进程数减半、内存下降。
    3. 启动前自动清理占用 8082/8083/8084/8085 的旧实例(含残留的 go run 父进程), 避免端口冲突。

  前置: Docker 内已起好 mysql(主机 3307)/redis(主机 6380)/kafka(主机 9192, EXTERNAL listener)。
  用法:
    pwsh scripts/start-local-m2.ps1
#>
param()

$root = Split-Path $PSScriptRoot -Parent   # 脚本位于 <root>/scripts/, 取上级即为项目根(避免硬编码绝对路径, 提升可移植性)
# 本地数据库口令: 优先取环境变量 ONEPARK_DB_PW, 否则从被 gitignore 的 deploy/.env 读取(避免口令入库);
# 与 deploy/.env 的 MYSQL_ROOT_PASSWORD / REDIS_PASSWORD 保持一致(本地容器口令).
$pw = $env:ONEPARK_DB_PW
if (-not $pw) {
  $envFile = Join-Path $root 'deploy\.env'
  if (Test-Path $envFile) {
    foreach ($line in (Get-Content $envFile)) {
      if ($line -match '^MYSQL_ROOT_PASSWORD=(.+)$') { $pw = $Matches[1].Trim(); break }
    }
  }
}
if (-not $pw) { Write-Error '未找到本地数据库口令: 请设置环境变量 ONEPARK_DB_PW 或在 deploy/.env 配置 MYSQL_ROOT_PASSWORD'; exit 1 }

# ---- 注入本地连接配置(对应各 app/*/etc/*-api.yaml 的 ${...} 占位符) ----
$env:REDIS_ADDR = '127.0.0.1:6380'
$env:REDIS_PASS = $pw
$env:KAFKA_BROKERS = '127.0.0.1:9192'
$dsn = "root:$pw@tcp(127.0.0.1:3307)/{0}?charset=utf8mb4&parseTime=True&loc=Local"
$env:WORKORDER_MYSQL_DSN = $dsn -f 'workorder_db'
$env:VISITOR_MYSQL_DSN   = $dsn -f 'visitor_db'
$env:PARKING_MYSQL_DSN   = $dsn -f 'parking_db'
$env:NOTICE_MYSQL_DSN    = $dsn -f 'notice_db'
# MinIO 本地未部署, 置空使附件上传接口返回"对象存储未配置"(不影响其余业务)
$env:WORKORDER_MINIO_ENDPOINT     = ''
$env:WORKORDER_MINIO_ACCESS_KEY   = 'admin'
$env:WORKORDER_MINIO_SECRET_KEY   = $pw
$env:WORKORDER_MINIO_PUBLIC_URL   = ''
# visitor-service 经 gRPC 调 device-service(本地未起, 仅访客开门类调用会报错, 不影响启动与其余接口)
$env:DEVICE_RPC_ENDPOINTS = '127.0.0.1:9001'

# 4 个 M2 服务: 名称 -> 监听端口
$svcs = @(
  @{ name = 'workorder'; port = 8082 },
  @{ name = 'visitor';   port = 8083 },
  @{ name = 'parking';   port = 8084 },
  @{ name = 'notice';    port = 8085 }
)

Set-Location $root

# ---- 1) 编译(每次都 build; go 构建缓存使增量编译仅 3~6s, 且能保证源码改动必重建) ----
# 注意: 不要做 "仅比对 go.mod 时间就跳过" 的优化 —— 改 .go 源码却不改 go.mod 时会漏编译.
foreach ($s in $svcs) {
  $n   = $s.name
  $exe = "$root\bin\$n.exe"
  Write-Host "[build] $n ..."
  go build -trimpath -ldflags "-s -w" -o $exe "./app/$n-service"
  if ($LASTEXITCODE -ne 0) { Write-Error "build $n failed"; exit 1 }
}

# ---- 2) 清理旧实例, 避免端口冲突 ----
# 2a) 杀掉占用目标端口的进程(旧的二进制或 go run 拉起的临时二进制)
foreach ($s in $svcs) {
  $p = $s.port
  Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue |
    ForEach-Object {
      $proc = Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue
      if ($proc) { Write-Host "[stop] kill PID $($proc.Id) on :$p"; Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    }
}
# 2b) 杀掉残留的 go run 父进程(其命令行含 run + app/*-service 路径)
Get-CimInstance Win32_Process -Filter "Name='go.exe'" -ErrorAction SilentlyContinue |
  Where-Object { $_.CommandLine -like '*run*' -and ($_.CommandLine -like '*app\*-service*' -or $_.CommandLine -like '*app/*-service*') } |
  ForEach-Object { Write-Host "[stop] go run PID $($_.ProcessId)"; Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }

# ---- 3) 启动预编译二进制 ----
foreach ($s in $svcs) {
  $n   = $s.name
  $exe = "$root\bin\$n.exe"
  $cfg = "$root\app\$n-service\etc\$n-api.yaml"
  Write-Host "[start] $n -> $exe -f $cfg"
  Start-Process -FilePath $exe -ArgumentList "-f", "$cfg" `
    -RedirectStandardOutput "$root\$n.log" -RedirectStandardError "$root\$n.err" `
    -WindowStyle Hidden
}

# ---- 4) 探测端口就绪 ----
Write-Host "waiting for ports..."
Start-Sleep -Seconds 6
foreach ($s in $svcs) {
  $r = Test-NetConnection -ComputerName 127.0.0.1 -Port $s.port -WarningAction SilentlyContinue
  Write-Host "PORT $($s.port) => $($r.TcpTestSucceeded)"
}
Write-Host "done."
