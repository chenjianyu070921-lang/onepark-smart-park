module onepark/app/gateway-service

go 1.26.0

// 本地模块重定向: 使本模块脱离 go.work(如 GOWORK=off / IDE 未启用 workspace)时仍可独立编译.
require onepark/common v0.0.0

replace onepark/common => ../../common

require (
	github.com/google/uuid v1.6.0
	github.com/pion/dtls/v3 v3.1.2
	github.com/plgd-dev/go-coap/v3 v3.5.4
	github.com/zeromicro/go-zero v1.10.3
	golang.org/x/crypto v0.57.0
	gorm.io/driver/mysql v1.6.0
	gorm.io/gorm v1.31.2
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dsnet/golib/memfile v1.0.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v4 v4.0.1 // indirect
	github.com/segmentio/kafka-go v0.4.51 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/titanous/json5 v1.0.0 // indirect
	go.opentelemetry.io/otel v1.40.0 // indirect
	go.opentelemetry.io/otel/trace v1.40.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/automaxprocs v1.6.0 // indirect
	golang.org/x/exp v0.0.0-20240904232852-e7e105dedf7e // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
