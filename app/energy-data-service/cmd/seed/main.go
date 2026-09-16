// 临时脚本: 往 energy_reading 表塞假数据, 用来验证表结构和入库代码是否通了
// 用法: cd app/energy-data-service && go run ./cmd/seed -f etc/energydata-api.yaml
// 等 M1 的 Kafka 数据接进来之后, 这个目录可以整个删掉
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"onepark/app/energy-data-service/internal/config"
	"onepark/app/energy-data-service/internal/model"
)

type fakeDevice struct {
	deviceID string
	zoneID   string
	baseKwh  float64
}

func main() {
	var configFile string
	flag.StringVar(&configFile, "f", "etc/energydata-api.yaml", "config file")
	flag.Parse()

	var c config.Config
	conf.MustLoad(configFile, &c)

	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		panic(err)
	}

	m := model.NewEnergyReadingModel(db)
	ctx := context.Background()

	devices := []fakeDevice{
		{deviceID: "METER-A01", zoneID: "A栋", baseKwh: 12000},
		{deviceID: "METER-B01", zoneID: "B栋", baseKwh: 8000},
	}

	now := time.Now()
	for _, d := range devices {
		for i := 0; i < 5; i++ {
			power := 1.0 + float64(i)*0.3
			reading := &model.EnergyReading{
				DeviceID:   d.deviceID,
				ZoneID:     d.zoneID,
				EnergyKwh:  d.baseKwh + float64(i)*2.5,
				PowerKw:    &power,
				ReportedAt: now.Add(-time.Duration(5-i) * 10 * time.Minute),
			}
			if err := m.Insert(ctx, reading); err != nil {
				panic(err)
			}
		}
		fmt.Printf("已写入 %s(%s) 5 条\n", d.deviceID, d.zoneID)
	}

	fmt.Println("---- 回读验证 ----")
	for _, d := range devices {
		latest, err := m.FindLatest(ctx, d.deviceID)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s 最新读数: %.2f 度, 采集时间 %s\n",
			latest.DeviceID, latest.EnergyKwh, latest.ReportedAt.Format("15:04:05"))
	}

	var total int64
	if err := db.Model(&model.EnergyReading{}).Count(&total).Error; err != nil {
		panic(err)
	}
	fmt.Printf("energy_reading 表现在共 %d 行\n", total)
}
