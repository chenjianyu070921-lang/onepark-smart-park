package cron

import (
	"context"

	"onepark/app/leasing-service/internal/logic/lease"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
)

// RunBillOnce 为所有「生效中」合同生成**上一自然月**的租金账单.
//
// 刻意复用 lease.BillAutoLogic 而不是在本包重写一遍:
// 账单生成涉及"锁 + 唯一索引幂等 + 单条失败不中断整批"三件事, 复制一份就等于给
// 同一个业务留了两个真相来源 —— 将来改计费口径必然漏改一个。
// 定时任务与手工接口(POST /api/lease/bill/auto)共用同一实现, 行为一致。
//
// 账期留空即取上一自然月(BillAutoLogic 内部规则): 每月 1 号 00:10 触发时,
// "上一自然月"正是刚刚结束的那个月。
func RunBillOnce(ctx context.Context, svcCtx *svc.ServiceContext) (*types.BillAutoResp, error) {
	return lease.NewBillAutoLogic(ctx, svcCtx).BillAuto(&types.BillAutoReq{})
}
