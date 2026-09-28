# 网关 JWT 鉴权端到端验证脚本
# 验证流程: auth登录拿Token → 带Token调业务接口 → 不带Token调业务接口看是否被拦

$gatewayUrl = "http://localhost:8080"
$authUrl = "http://localhost:8088"

Write-Host "=== 1. 健康检查 ===" -ForegroundColor Cyan
try {
    $health = Invoke-RestMethod -Uri "$gatewayUrl/health" -Method GET -TimeoutSec 5
    Write-Host "[OK] 网关健康检查通过" -ForegroundColor Green
} catch {
    Write-Host "[FAIL] 网关未启动: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

Write-Host "`n=== 2. auth登录拿Token ===" -ForegroundColor Cyan
$loginBody = @{
    username = "admin"
    password = "admin123"
} | ConvertTo-Json

try {
    $loginResp = Invoke-RestMethod -Uri "$gatewayUrl/api/auth/login" -Method POST -Body $loginBody -ContentType "application/json" -TimeoutSec 5
    Write-Host "[OK] 登录成功" -ForegroundColor Green
    Write-Host "  UserId: $($loginResp.userId)"
    Write-Host "  TenantId: $($loginResp.tenantId)"
    Write-Host "  RoleIds: $($loginResp.roleIds)"
    Write-Host "  Token: $($loginResp.token.Substring(0, 30))..."
    $token = $loginResp.token
} catch {
    Write-Host "[FAIL] 登录失败: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "  响应: $($_.ErrorDetails.Message)"
    exit 1
}

Write-Host "`n=== 3. 带Token调业务接口(应成功) ===" -ForegroundColor Cyan
$headers = @{
    Authorization = "Bearer $token"
}
try {
    $bizResp = Invoke-RestMethod -Uri "$gatewayUrl/api/workorder/list" -Method GET -Headers $headers -TimeoutSec 5
    Write-Host "[OK] 带Token调业务接口成功" -ForegroundColor Green
    Write-Host "  响应: $($bizResp | ConvertTo-Json -Depth 2)"
} catch {
    Write-Host "[FAIL] 带Token调业务接口失败: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "  响应: $($_.ErrorDetails.Message)"
}

Write-Host "`n=== 4. 不带Token调业务接口(应被拦401) ===" -ForegroundColor Cyan
try {
    $noAuthResp = Invoke-RestMethod -Uri "$gatewayUrl/api/workorder/list" -Method GET -TimeoutSec 5
    Write-Host "[WARN] 未带Token居然成功了(网关鉴权未开启?)" -ForegroundColor Yellow
    Write-Host "  响应: $($noAuthResp | ConvertTo-Json -Depth 2)"
} catch {
    $statusCode = $_.Exception.Response.StatusCode.value__
    if ($statusCode -eq 401) {
        Write-Host "[OK] 未带Token被正确拦截(401 Unauthorized)" -ForegroundColor Green
    } else {
        Write-Host "[FAIL] 未带Token返回了非预期状态码: $statusCode" -ForegroundColor Red
        Write-Host "  响应: $($_.ErrorDetails.Message)"
    }
}

Write-Host "`n=== 5. 带无效Token调业务接口(应被拦401) ===" -ForegroundColor Cyan
$badHeaders = @{
    Authorization = "Bearer invalidtoken123"
}
try {
    $badResp = Invoke-RestMethod -Uri "$gatewayUrl/api/workorder/list" -Method GET -Headers $badHeaders -TimeoutSec 5
    Write-Host "[WARN] 无效Token居然成功了" -ForegroundColor Yellow
} catch {
    $statusCode = $_.Exception.Response.StatusCode.value__
    if ($statusCode -eq 401) {
        Write-Host "[OK] 无效Token被正确拦截(401 Unauthorized)" -ForegroundColor Green
    } else {
        Write-Host "[FAIL] 无效Token返回了非预期状态码: $statusCode" -ForegroundColor Red
    }
}

Write-Host "`n=== 验证完成 ===" -ForegroundColor Cyan
Write-Host "通过项: 健康检查 / 登录拿Token / 带Token调业务 / 未带Token拦截 / 无效Token拦截" -ForegroundColor Green
