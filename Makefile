# Go 编译并行数（Windows 上默认值太高容易导致 "Insufficient system resources" 错误）
export GOFLAGS=-p=2

# ============ HTTP 服务 goctl 生成命令 ============
# 生成「设备服务」go-zero HTTP 代码（依据 app/device-service/device.api）
device-goctl:
	goctl api go -api app/device-service/device.api -dir app/device-service --style=gozero

# 生成「工单服务」go-zero HTTP 代码（依据 app/workorder-service/workorder.api）
workorder-goctl:
	goctl api go -api app/workorder-service/workorder.api -dir app/workorder-service --style=gozero

# 生成「访客服务」go-zero HTTP 代码（依据 app/visitor-service/visitor.api）
visitor-goctl:
	goctl api go -api app/visitor-service/visitor.api -dir app/visitor-service --style=gozero

# 生成「停车服务」go-zero HTTP 代码（依据 app/parking-service/parking.api）
parking-goctl:
	goctl api go -api app/parking-service/parking.api -dir app/parking-service --style=gozero

# 生成「通知服务」go-zero HTTP 代码（依据 app/notice-service/notice.api）
notice-goctl:
	goctl api go -api app/notice-service/notice.api -dir app/notice-service --style=gozero

# 生成「告警服务」go-zero HTTP 代码（依据 app/alarm-service/alarm.api）
alarm-goctl:
	goctl api go -api app/alarm-service/alarm.api -dir app/alarm-service --style=gozero

# 生成「门禁服务」go-zero HTTP 代码（依据 app/access-control-service/accesscontrol.api）
accesscontrol-goctl:
	goctl api go -api app/access-control-service/accesscontrol.api -dir app/access-control-service --style=gozero

# 生成「视频服务」go-zero HTTP 代码（依据 app/video-service/video.api）
video-goctl:
	goctl api go -api app/video-service/video.api -dir app/video-service --style=gozero

# 生成「能耗数据服务」go-zero HTTP 代码（依据 app/energy-data-service/energydata.api）
energydata-goctl:
	goctl api go -api app/energy-data-service/energydata.api -dir app/energy-data-service --style=gozero

# 生成「能耗分析服务」go-zero HTTP 代码（依据 app/energy-analysis-service/energyanalysis.api）
energyanalysis-goctl:
	goctl api go -api app/energy-analysis-service/energyanalysis.api -dir app/energy-analysis-service --style=gozero

# 生成「计费服务」go-zero HTTP 代码（依据 app/billing-service/billing.api）
billing-goctl:
	goctl api go -api app/billing-service/billing.api -dir app/billing-service --style=gozero

# 生成「租赁服务」go-zero HTTP 代码（依据 app/leasing-service/leasing.api）
leasing-goctl:
	goctl api go -api app/leasing-service/leasing.api -dir app/leasing-service --style=gozero

# 生成「大屏服务」go-zero HTTP 代码（依据 app/dashboard-service/dashboard.api）
dashboard-goctl:
	goctl api go -api app/dashboard-service/dashboard.api -dir app/dashboard-service --style=gozero

# 生成「调度服务」go-zero HTTP 代码（依据 app/dispatch-service/dispatch.api）
dispatch-goctl:
	goctl api go -api app/dispatch-service/dispatch.api -dir app/dispatch-service --style=gozero

# 生成「认证服务」go-zero HTTP 代码（依据 app/auth-service/auth.api）
auth-goctl:
	goctl api go -api app/auth-service/auth.api -dir app/auth-service --style=gozero

# 生成「用户管理服务」go-zero HTTP 代码（依据 app/user-manage/usermanage.api）
usermanage-goctl:
	goctl api go -api app/user-manage/usermanage.api -dir app/user-manage --style=gozero

# 对外入口（根目录 gateway/）
# 生成「根目录 API 网关」go-zero HTTP 代码（依据 gateway/apigateway.api，对外统一入口）
apigateway-goctl:
	goctl api go -api gateway/apigateway.api -dir gateway --style=gozero

# ============ gRPC 服务 goctl 生成命令 ============
# 生成「shadow 服务」go-zero gRPC(zrpc) 代码（依据 app/shadow-service/shadow.proto）
shadow-goctl:
	goctl rpc protoc app/shadow-service/shadow.proto \
	--go_out=app/shadow-service/shadow \
	--go-grpc_out=app/shadow-service/shadow \
	--zrpc_out=app/shadow-service \
	--style=gozero

# ============ HTTP 服务 main 启动命令 ============
# 启动「设备服务」HTTP（go run，读取 etc/device-api.yaml 配置）
run-device:
	go run .\app\device-service\device.go -f .\app\device-service\etc\device-api.yaml

# 启动「工单服务」HTTP（go run，读取 etc/workorder-api.yaml 配置）
run-workorder:
	go run .\app\workorder-service\workorder.go -f .\app\workorder-service\etc\workorder-api.yaml

# 启动「访客服务」HTTP（go run，读取 etc/visitor-api.yaml 配置）
run-visitor:
	go run .\app\visitor-service\visitor.go -f .\app\visitor-service\etc\visitor-api.yaml

# 启动「停车服务」HTTP（go run，读取 etc/parking-api.yaml 配置）
run-parking:
	go run .\app\parking-service\parking.go -f .\app\parking-service\etc\parking-api.yaml

# 启动「通知服务」HTTP（go run，读取 etc/notice-api.yaml 配置）
run-notice:
	go run .\app\notice-service\notice.go -f .\app\notice-service\etc\notice-api.yaml

# 启动「告警服务」HTTP（go run，读取 etc/alarm-api.yaml 配置）
run-alarm:
	go run .\app\alarm-service\alarm.go -f .\app\alarm-service\etc\alarm-api.yaml

# 启动「门禁服务」HTTP（go run，读取 etc/accesscontrol-api.yaml 配置）
run-accesscontrol:
	go run .\app\access-control-service\accesscontrol.go -f .\app\access-control-service\etc\accesscontrol-api.yaml

# 启动「视频服务」HTTP（go run，读取 etc/video-api.yaml 配置）
run-video:
	go run .\app\video-service\video.go -f .\app\video-service\etc\video-api.yaml

# 启动「能耗数据服务」HTTP（go run，读取 etc/energydata-api.yaml 配置）
run-energydata:
	go run .\app\energy-data-service\energydata.go -f .\app\energy-data-service\etc\energydata-api.yaml

# 启动「能耗分析服务」HTTP（go run，读取 etc/energyanalysis-api.yaml 配置）
run-energyanalysis:
	go run .\app\energy-analysis-service\energyanalysis.go -f .\app\energy-analysis-service\etc\energyanalysis-api.yaml

# 启动「计费服务」HTTP（go run，读取 etc/billing-api.yaml 配置）
run-billing:
	go run .\app\billing-service\billing.go -f .\app\billing-service\etc\billing-api.yaml

# 启动「租赁服务」HTTP（go run，读取 etc/leasing-api.yaml 配置）
run-leasing:
	go run .\app\leasing-service\leasing.go -f .\app\leasing-service\etc\leasing-api.yaml

# 启动「大屏服务」HTTP（go run，读取 etc/dashboard-api.yaml 配置）
run-dashboard:
	go run .\app\dashboard-service\dashboard.go -f .\app\dashboard-service\etc\dashboard-api.yaml

# 启动「调度服务」HTTP（go run，读取 etc/dispatch-api.yaml 配置）
run-dispatch:
	go run .\app\dispatch-service\dispatch.go -f .\app\dispatch-service\etc\dispatch-api.yaml

# 启动「认证服务」HTTP（go run，读取 etc/auth-api.yaml 配置）
run-auth:
	go run .\app\auth-service\auth.go -f .\app\auth-service\etc\auth-api.yaml

# 启动「用户管理服务」HTTP（go run，读取 etc/usermanage-api.yaml 配置）
run-usermanage:
	go run .\app\user-manage\usermanage.go -f .\app\user-manage\etc\usermanage-api.yaml

# 对外入口（根目录 gateway/，HTTP :8080）
# 启动「根目录 API 网关」HTTP（go run，读取 gateway/etc/apigateway-api.yaml，对外统一入口 :8080）
run-apigateway:
	go run .\gateway\apigateway.go -f .\gateway\etc\apigateway-api.yaml

# ============ gRPC 服务 main 启动命令 ============
# 启动「shadow 服务」gRPC（go run，读取 etc/shadow.yaml 配置）
run-shadow:
	go run .\app\shadow-service\shadow.go -f .\app\shadow-service\etc\shadow.yaml

# 启动「告警服务」gRPC（go run，读取 etc/alarm-grpc.yaml；与 run-alarm 的 HTTP 端口不同，二者可同时跑）
run-alarm-grpc:
	go run .\app\alarm-service\alarm.go -f .\app\alarm-service\etc\alarm-grpc.yaml

# ============ 设备网关与后台进程启动命令 ============
# 启动「设备网关」后台进程（go run，读取 etc/gateway.yaml；负责 MQTT/EMQX 等设备接入）
run-gateway-service:
	go run .\app\gateway-service\gateway.go -f .\app\gateway-service\etc\gateway.yaml

# 启动「事件分发」后台进程（go run，读取 etc/dispatcher.yaml；Kafka→MQTT/gRPC 事件投递）
run-event-dispatcher:
	go run .\app\event-dispatcher\dispatcher.go -f .\app\event-dispatcher\etc\dispatcher.yaml

# ============ 通用工具与中间件 ============
# 安装代码生成依赖到 GOPATH/bin：goctl + protoc-gen-go + protoc-gen-go-grpc
# 注意：仅装插件，protoc 二进制本体需本机另行预装（genproto 依赖）
install-tools:
	go install github.com/zeromicro/go-zero/tools/goctl@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# 统一 proto 契约生成：所有 .pb.go 落在 proto/<svc>/ （独立 module onepark/proto）
# 一条 protoc 调用处理全部 14 个 proto，跨文件 import 自动解析
# 用 protoc 统一生成全部 14 个服务的 .pb.go / _grpc.pb.go 到 proto/<svc>/
# 独立 module onepark/proto；一条命令处理跨文件 import。需本机预装 protoc
genproto:
	cd proto && protoc --proto_path=. \
	--go_out=. --go_opt=module=onepark/proto \
	--go-grpc_out=. --go-grpc_opt=module=onepark/proto \
	common/common.proto \
	device/device.proto shadow/shadow.proto workorder/workorder.proto \
	access/access.proto alarm/alarm.proto video/video.proto \
	energy/energy.proto billing/billing.proto auth/auth.proto user/user.proto \
	leasing/leasing.proto dispatch/dispatch.proto dashboard/dashboard.proto

# app/* 下每个子目录编译为 bin/<目录名>; Windows(cmd) 与 sh 均兼容的写法
# 编译 app/* 下每个服务为 bin/<目录名>，并编译根目录 gateway
build:
	@if not exist bin mkdir bin
	@for /d %%d in (app\*) do (echo building %%~nd... && go build -o bin\%%~nd .\%%d) || exit /b 1
	@echo building gateway... && go build -o bin\gateway .\gateway

# 运行全仓单元测试（go test ./...）
test:
	go test ./...

# 用 docker compose 后台(-d)拉起整套依赖/服务（deploy/docker-compose.yml + deploy/.env）
up:
	docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d

# 停止并移除上述 compose 容器
down:
	docker compose -f deploy/docker-compose.yml down

# 删除编译产物 bin/ 目录
clean:
	@if exist bin rd /s /q bin

.PHONY: device-goctl workorder-goctl visitor-goctl parking-goctl notice-goctl alarm-goctl accesscontrol-goctl video-goctl energydata-goctl energyanalysis-goctl billing-goctl leasing-goctl dashboard-goctl dispatch-goctl auth-goctl usermanage-goctl apigateway-goctl shadow-goctl run-device run-workorder run-visitor run-parking run-notice run-alarm run-accesscontrol run-video run-energydata run-energyanalysis run-billing run-leasing run-dashboard run-dispatch run-auth run-usermanage run-apigateway run-shadow run-alarm-grpc run-gateway-service run-event-dispatcher install-tools genproto build test up down clean














