# 告警消息生产工具(包装脚本).
#
# 真正的实现在 app/dispatch-service/tools/alarmproducer/ —— 必须放在 Go 模块内才能 run:
# 仓库根目录没有 go.mod, deploy/ 不属于任何模块, 放在这里 go run 解析不了 onepark/common 的导入。
# 本脚本只是让调用方式与计划书保持一致(deploy/test/alarm_producer...)。
#
# 用法:
#   .\deploy\test\alarm_producer.ps1 -EventType fire -DeviceId SMOKE-001 -Zone A-1F-101
#   .\deploy\test\alarm_producer.ps1 -Raw '{"device_id":"x"}'      # 毒消息: 缺 request_id
#   .\deploy\test\alarm_producer.ps1 -RequestId req-fixed-1 -Count 2   # 同 id 复投, 验证幂等
param(
    [string]$Brokers = "127.0.0.1:19092",
    [string]$Topic = "alarm-event",
    [string]$EventType = "fire",
    [string]$DeviceId = "",
    [string]$Zone = "",
    [string]$RequestId = "",
    [string]$Raw = "",
    [int]$Count = 1,
    [int]$IntervalMs = 0
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)

$argsList = @(
    "run", "./app/dispatch-service/tools/alarmproducer",
    "-brokers", $Brokers,
    "-topic", $Topic,
    "-event-type", $EventType,
    "-device-id", $DeviceId,
    "-zone", $Zone,
    "-count", $Count,
    "-interval-ms", $IntervalMs
)
if ($Raw) { $argsList += @("-raw", $Raw) }
if ($RequestId) { $argsList += @("-request-id", $RequestId) }

Push-Location $root
try {
    & go @argsList
    exit $LASTEXITCODE
}
finally {
    Pop-Location
}
