# Go 编译并行数（Windows 上默认值太高容易导致 "Insufficient system resources" 错误）
export GOFLAGS=-p=2

# ============ HTTP 服务 goctl 生成命令 ============
device-goctl:
	goctl api go -api app/device-service/device.api -dir app/device-service --style=gozero

workorder-goctl:
	goctl api go -api app/workorder-service/workorder.api -dir app/workorder-service --style=gozero

visitor-goctl:
	goctl api go -api app/visitor-service/visitor.api -dir app/visitor-service --style=gozero

parking-goctl:
	goctl api go -api app/parking-service/parking.api -dir app/parking-service --style=gozero

notice-goctl:
	goctl api go -api app/notice-service/notice.api -dir app/notice-service --style=gozero

alarm-goctl:
	goctl api go -api app/alarm-service/alarm.api -dir app/alarm-service --style=gozero

accesscontrol-goctl:
	goctl api go -api app/access-control-service/accesscontrol.api -dir app/access-control-service --style=gozero

video-goctl:
	goctl api go -api app/video-service/video.api -dir app/video-service --style=gozero

energydata-goctl:
	goctl api go -api app/energy-data-service/energydata.api -dir app/energy-data-service --style=gozero

energyanalysis-goctl:
	goctl api go -api app/energy-analysis-service/energyanalysis.api -dir app/energy-analysis-service --style=gozero

billing-goctl:
	goctl api go -api app/billing-service/billing.api -dir app/billing-service --style=gozero

leasing-goctl:
	goctl api go -api app/leasing-service/leasing.api -dir app/leasing-service --style=gozero

dashboard-goctl:
	goctl api go -api app/dashboard-service/dashboard.api -dir app/dashboard-service --style=gozero

dispatch-goctl:
	goctl api go -api app/dispatch-service/dispatch.api -dir app/dispatch-service --style=gozero

auth-goctl:
	goctl api go -api app/auth-service/auth.api -dir app/auth-service --style=gozero

usermanage-goctl:
	goctl api go -api app/user-manage/usermanage.api -dir app/user-manage --style=gozero

# 对外入口（根目录 gateway/）
apigateway-goctl:
	goctl api go -api gateway/apigateway.api -dir gateway --style=gozero

# ============ gRPC 服务 goctl 生成命令 ============
shadow-goctl:
	goctl rpc protoc app/shadow-service/shadow.proto \
	--go_out=app/shadow-service/shadow \
	--go-grpc_out=app/shadow-service/shadow \
	--zrpc_out=app/shadow-service \
	--style=gozero

# ============ HTTP 服务 main 启动命令 ============
run-device:
	go run .\app\device-service\device.go -f .\app\device-service\etc\device-api.yaml

run-workorder:
	go run .\app\workorder-service\workorder.go -f .\app\workorder-service\etc\workorder-api.yaml

run-visitor:
	go run .\app\visitor-service\visitor.go -f .\app\visitor-service\etc\visitor-api.yaml

run-parking:
	go run .\app\parking-service\parking.go -f .\app\parking-service\etc\parking-api.yaml

run-notice:
	go run .\app\notice-service\notice.go -f .\app\notice-service\etc\notice-api.yaml

run-alarm:
	go run .\app\alarm-service\alarm.go -f .\app\alarm-service\etc\alarm-api.yaml

run-accesscontrol:
	go run .\app\access-control-service\accesscontrol.go -f .\app\access-control-service\etc\accesscontrol-api.yaml

run-video:
	go run .\app\video-service\video.go -f .\app\video-service\etc\video-api.yaml

run-energydata:
	go run .\app\energy-data-service\energydata.go -f .\app\energy-data-service\etc\energydata-api.yaml

run-energyanalysis:
	go run .\app\energy-analysis-service\energyanalysis.go -f .\app\energy-analysis-service\etc\energyanalysis-api.yaml

run-billing:
	go run .\app\billing-service\billing.go -f .\app\billing-service\etc\billing-api.yaml

run-leasing:
	go run .\app\leasing-service\leasing.go -f .\app\leasing-service\etc\leasing-api.yaml

run-dashboard:
	go run .\app\dashboard-service\dashboard.go -f .\app\dashboard-service\etc\dashboard-api.yaml

run-dispatch:
	go run .\app\dispatch-service\dispatch.go -f .\app\dispatch-service\etc\dispatch-api.yaml

run-auth:
	go run .\app\auth-service\auth.go -f .\app\auth-service\etc\auth-api.yaml

run-usermanage:
	go run .\app\user-manage\usermanage.go -f .\app\user-manage\etc\usermanage-api.yaml

# 对外入口（根目录 gateway/，HTTP :8080）
run-apigateway:
	go run .\gateway\apigateway.go -f .\gateway\etc\apigateway-api.yaml

# ============ gRPC 服务 main 启动命令 ============
run-shadow:
	go run .\app\shadow-service\shadow.go -f .\app\shadow-service\etc\shadow.yaml

# ============ 设备网关与后台进程启动命令 ============
run-gateway-service:
	go run .\app\gateway-service\gateway.go -f .\app\gateway-service\etc\gateway.yaml

run-event-dispatcher:
	go run .\app\event-dispatcher\dispatcher.go -f .\app\event-dispatcher\etc\dispatcher.yaml

# ============ 通用工具与中间件 ============
install-tools:
	go install github.com/zeromicro/go-zero/tools/goctl@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# 统一 proto 契约生成：所有 .pb.go 落在 proto/<svc>/ （独立 module onepark/proto）
# 一条 protoc 调用处理全部 14 个 proto，跨文件 import 自动解析
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
build:
	@if not exist bin mkdir bin
	@for /d %%d in (app\*) do (echo building %%~nd... && go build -o bin\%%~nd .\%%d) || exit /b 1
	@echo building gateway... && go build -o bin\gateway .\gateway

test:
	go test ./...

up:
	docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d

down:
	docker compose -f deploy/docker-compose.yml down

clean:
	@if exist bin rd /s /q bin

.PHONY: device-goctl workorder-goctl visitor-goctl parking-goctl notice-goctl alarm-goctl accesscontrol-goctl video-goctl energydata-goctl energyanalysis-goctl billing-goctl leasing-goctl dashboard-goctl dispatch-goctl auth-goctl usermanage-goctl apigateway-goctl shadow-goctl run-device run-workorder run-visitor run-parking run-notice run-alarm run-accesscontrol run-video run-energydata run-energyanalysis run-billing run-leasing run-dashboard run-dispatch run-auth run-usermanage run-apigateway run-shadow run-alarm-grpc run-gateway-service run-event-dispatcher install-tools genproto build test up down clean














