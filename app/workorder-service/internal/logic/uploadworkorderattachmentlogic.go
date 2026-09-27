package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/gormx"
	"onepark/common/minio"

	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UploadWorkOrderAttachmentLogic 工单附件上传逻辑.
type UploadWorkOrderAttachmentLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUploadWorkOrderAttachmentLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UploadWorkOrderAttachmentLogic {
	return &UploadWorkOrderAttachmentLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// UploadWorkOrderAttachment 校验工单(租户隔离) → 上传 MinIO → 落附件表 → 同步主表 attachments.
func (l *UploadWorkOrderAttachmentLogic) UploadWorkOrderAttachment(req *types.UploadWorkOrderAttachmentReq) (resp *types.UploadWorkOrderAttachmentResp, err error) {
	if l.svcCtx.MinIO == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "对象存储未配置")
	}
	tenantID := ctxdata.GetTenantId(l.ctx)

	// RBAC 行级隔离: 工单必须属于当前租户.
	var wo model.WorkOrder
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("id=? AND tenant_id=?", req.Id, tenantID).First(&wo).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrWorkOrderNotFound, "工单不存在")
		}
		l.Errorf("load work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载工单失败")
	}

	// 参数校验: 文件非空且不超过 20MB.
	if req.File.Size <= 0 {
		return nil, errorx.NewError(errorx.ErrM2ParamInvalid, "上传文件为空")
	}
	const maxSize = 20 << 20 // 20MB
	if req.File.Size > maxSize {
		return nil, errorx.NewError(errorx.ErrM2ParamInvalid, "文件超过 20MB 上限")
	}

	src, err := req.File.Open()
	if err != nil {
		l.Errorf("open upload file failed: %v", err)
		return nil, errorx.NewError(errorx.ErrM2Internal, "读取上传文件失败")
	}
	defer src.Close()

	ext := path.Ext(req.File.Filename)
	// 对象Key: workorder/{tenant}/{woId}/{uuid}{ext}, 租户隔离 + 防覆盖.
	objectKey := fmt.Sprintf("workorder/%d/%d/%s%s", tenantID, wo.ID, uuid.NewString(), ext)

	contentType := req.File.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if _, err = miniox.PutObject(l.ctx, l.svcCtx.MinIO, l.svcCtx.Config.MinIO.Bucket, objectKey, src, req.File.Size, contentType); err != nil {
		l.Errorf("minio put object failed: %v", err)
		return nil, errorx.NewError(errorx.ErrM2Internal, "上传对象存储失败")
	}

	att := model.WorkOrderAttachment{
		WorkOrderID: wo.ID,
		ObjectKey:   objectKey,
		FileName:    req.File.Filename,
	}
	if e := l.svcCtx.DB.WithContext(l.ctx).Create(&att).Error; e != nil {
		l.Errorf("save attachment record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "保存附件记录失败")
	}

	// 聚合该工单全部附件 object_key 写回主表 attachments 列, 使 GetWorkOrder 直接返回最新列表.
	// 同步失败必须上抛(原实现仅记日志返回成功, 会让 GetWorkOrder 返回不完整的附件列表, 审查问题3).
	if e := l.syncAttachments(tenantID, wo.ID); e != nil {
		l.Errorf("sync work order attachments failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "同步附件列表失败")
	}

	return &types.UploadWorkOrderAttachmentResp{
		AttachmentId: att.ID,
		ObjectKey:    objectKey,
		FileName:     req.File.Filename,
	}, nil
}

// syncAttachments 把指定工单的全部附件 object_key 聚合为 JSON 写回 work_order.attachments.
// 修复: 原实现为"读全部附件→写回主表"两步非原子, 并发上传会出现读-改-写竞争导致后写的缓存列覆盖先写,
// 造成附件漏显(审查问题3). 这里包在事务内并对主表行加 FOR UPDATE 锁, 串行化同单的缓存列更新;
// 同时 json.Marshal 错误不再被忽略.
func (l *UploadWorkOrderAttachmentLogic) syncAttachments(tenantID, woID int64) error {
	return l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gormx.DB) error {
		// 行锁主表, 阻止并发上传对该单 attachments 列的读-改-写竞争.
		var wo model.WorkOrder
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id=? AND tenant_id=?", woID, tenantID).First(&wo).Error; e != nil {
			return e
		}
		var atts []model.WorkOrderAttachment
		if e := tx.Where("work_order_id=?", woID).Find(&atts).Error; e != nil {
			return e
		}
		keys := make([]string, 0, len(atts))
		for _, a := range atts {
			keys = append(keys, a.ObjectKey)
		}
		b, err := json.Marshal(keys)
		if err != nil {
			return err
		}
		return tx.Model(&model.WorkOrder{}).
			Where("id=? AND tenant_id=?", woID, tenantID).
			Update("attachments", string(b)).Error
	})
}
