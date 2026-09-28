param([string]$Name, [string]$Db)
$root = 'C:\Users\21848\Desktop\park\onepark-smart-park'
$pw = '4ay1nkal3u8ed77y'
$env:REDIS_ADDR = '115.191.16.159:6379'
$env:REDIS_PASS = $pw
$env:KAFKA_BROKERS = '115.191.16.159:9092'
$dsn = "root:$pw@tcp(115.191.16.159:3306)/${Db}?charset=utf8mb4&parseTime=True&loc=Local"

switch ($Name) {
  'visitor'   { $env:VISITOR_MYSQL_DSN = $dsn; $env:DEVICE_RPC_ENDPOINTS = '127.0.0.1:9001' }
  'parking'   { $env:PARKING_MYSQL_DSN = $dsn }
  'workorder' { $env:WORKORDER_MYSQL_DSN = $dsn; $env:WORKORDER_MINIO_ENDPOINT = ''; $env:WORKORDER_MINIO_ACCESS_KEY = 'admin'; $env:WORKORDER_MINIO_SECRET_KEY = 'onepark123'; $env:WORKORDER_MINIO_PUBLIC_URL = '' }
  'dashboard' { $env:DASHBOARD_MYSQL_DSN = $dsn; $env:WORKORDER_RPC_ENDPOINTS = '127.0.0.1:9093'; $env:DEVICE_RPC_ENDPOINTS = '127.0.0.1:9001'; $env:ALARM_RPC_ENDPOINTS = '127.0.0.1:9009'; $env:ENERGY_DATA_RPC_ENDPOINTS = '127.0.0.1:9061' }
}

"DSN_DEBUG name=$Name value=$dsn" | Out-File -FilePath "$root\bin\dsn_$Name.txt" -Encoding ascii
$exe = "$root\bin\$Name.exe"
if (-not (Test-Path $exe)) {
  Write-Error "exe not found: $exe"
  exit 1
}
$cfg = "$root\app\$Name-service\etc\$Name-api.yaml"
Set-Location $root
& $exe -f $cfg *>> "$root\bin\run_$Name.out.log" 2>&1
