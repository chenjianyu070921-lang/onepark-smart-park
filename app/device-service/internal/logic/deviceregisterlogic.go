package logic

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/model"
	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeviceRegisterLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeviceRegisterLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeviceRegisterLogic {
	return &DeviceRegisterLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeviceRegisterLogic) DeviceRegister(req *types.DeviceRegisterReq) (resp *types.DeviceRegisterResp, err error) {
	// 1. 查产品是否存在
	product, err := l.svcCtx.ProductModel.FindByKey(l.ctx, req.ProductKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(errorx.ErrProductNotFound, "产品不存在")
		}
		l.Errorf("查询产品失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询产品失败")
	}

	// 2. 设备名查重（同一产品下不允许重名）
	deviceName := req.DeviceName
	if deviceName == "" {
		deviceName = "device-" + uuid.NewString()[:8]
	} else {
		exist, _ := l.svcCtx.DeviceModel.FindByProductKeyAndName(l.ctx, req.ProductKey, deviceName)
		if exist != nil {
			return nil, errorx.NewError(errorx.ErrDeviceDuplicate, "同一产品下设备名已存在")
		}
	}

	// 3. 生成 deviceId 和明文密钥
	deviceID := uuid.NewString()
	plainSecret := genSecret(32)

	// 4. bcrypt 加密密钥
	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(plainSecret), bcrypt.DefaultCost)
	if err != nil {
		l.Errorf("密钥加密失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "密钥加密失败")
	}

	// 5. 写设备表
	device := &model.Device{
		DeviceID:     deviceID,
		DeviceName:   deviceName,
		DeviceSecret: string(hashedSecret),
		ProductKey:   product.ProductKey,
		ParkID:       req.ParkID,
		BuildingID:   req.BuildingID,
		Floor:        req.Floor,
		Location:     req.Location,
		Status:       0, // 0=未激活
	}
	if err := l.svcCtx.DeviceModel.Insert(l.ctx, device); err != nil {
		l.Errorf("设备写入失败: %v", err)
		return nil, errorx.NewError(errorx.ErrDeviceCreateFail, "设备写入失败")
	}

	// 6. 创建设备影子（空 desired/reported，version=0）
	shadow := &model.Shadow{
		DeviceID: deviceID,
		Desired:  datatypes.JSON([]byte(`{}`)),
		Reported: datatypes.JSON([]byte(`{}`)),
		Version:  0,
	}
	if err := l.svcCtx.ShadowModel.Insert(l.ctx, shadow); err != nil {
		l.Errorf("影子创建失败: %v", err)
		return nil, errorx.NewError(errorx.ErrShadowCreateFail, "影子创建失败")
	}

	// 7. 返回明文密钥（仅此一次）
	l.Infof("设备注册成功: deviceId=%s, productKey=%s", deviceID, req.ProductKey)

	return &types.DeviceRegisterResp{
		DeviceID:     deviceID,
		DeviceSecret: plainSecret,
	}, nil
}

// genSecret 生成 n 字节随机密钥，base64 编码返回
func genSecret(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b) + "_" + time.Now().Format("0102150405")
}
