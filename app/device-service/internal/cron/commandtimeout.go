// Package cron 提供 device-service 的常驻定时任务.
// 当前仅指令超时扫描: 超时未回执的指令先尝试重发(次数上限由 Redis 计数),
// 仍无回执则置为超时状态, 避免 command_log 长期悬挂在待发送/已下发.
package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"onepark/app/device-service/internal/model"
	"onepark/app/device-service/internal/svc"
	"onepark/common/mqtt"

	"github.com/zeromicro/go-zero/core/logx"
)

// 默认参数
const (
	defaultIntervalSec = 15
	defaultBatchLimit  = 200
	defaultMaxResend   = 2
	resendKeyTTL       = 3600 // 重发计数 TTL(秒)
)

// CommandTimeoutTask 指令超时扫描任务.
type CommandTimeoutTask struct {
	logx.Logger
	svcCtx    *svc.ServiceContext
	interval  time.Duration
	limit     int
	maxResend int
}

// NewCommandTimeoutTask 创建任务; 参数非正时使用默认值.
func NewCommandTimeoutTask(svcCtx *svc.ServiceContext, intervalSec, limit, maxResend int) *CommandTimeoutTask {
	if intervalSec <= 0 {
		intervalSec = defaultIntervalSec
	}
	if limit <= 0 {
		limit = defaultBatchLimit
	}
	if maxResend < 0 {
		maxResend = defaultMaxResend
	}
	return &CommandTimeoutTask{
		Logger:    logx.WithContext(context.Background()),
		svcCtx:    svcCtx,
		interval:  time.Duration(intervalSec) * time.Second,
		limit:     limit,
		maxResend: maxResend,
	}
}

// Start 启动扫描循环, 阻塞运行; ctx 取消即退出. 应在独立 goroutine 中调用.
func (t *CommandTimeoutTask) Start(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	logx.Infof("指令超时扫描已启动: interval=%s, limit=%d, maxResend=%d", t.interval, t.limit, t.maxResend)
	for {
		select {
		case <-ctx.Done():
			logx.Info("指令超时扫描已停止")
			return
		case <-ticker.C:
			t.scanOnce(ctx)
		}
	}
}

func (t *CommandTimeoutTask) scanOnce(ctx context.Context) {
	list, err := t.svcCtx.CommandLogModel.FindTimeoutList(ctx, t.limit)
	if err != nil {
		t.Errorf("查询超时指令失败: %v", err)
		return
	}
	if len(list) == 0 {
		return
	}

	for _, cmd := range list {
		timeoutSec := t.svcCtx.Config.CommandTimeoutSec
		if timeoutSec <= 0 {
			timeoutSec = 30
		}

		// 下行通道可用且未达重发上限 -> 重发并重新计时
		if t.svcCtx.Downlink != nil && t.resendCount(cmd.RequestID) < int64(t.maxResend) {
			if err := t.resend(ctx, cmd, timeoutSec); err != nil {
				t.Errorf("指令重发失败: requestId=%s, err=%v", cmd.RequestID, err)
				continue
			}
			t.Infof("指令已重发: requestId=%s, deviceId=%s", cmd.RequestID, cmd.DeviceID)
			continue
		}

		if err := t.svcCtx.CommandLogModel.MarkStatus(ctx, cmd.RequestID, model.CommandStatusTimeout); err != nil {
			t.Errorf("指令置超时失败: requestId=%s, err=%v", cmd.RequestID, err)
			continue
		}
		t.Infof("指令超时: requestId=%s, deviceId=%s, commandType=%s",
			cmd.RequestID, cmd.DeviceID, cmd.CommandType)
	}
}

func (t *CommandTimeoutTask) resend(ctx context.Context, cmd *model.CommandLog, timeoutSec int) error {
	payload := []byte(`{}`)
	if len(cmd.Payload) > 0 {
		payload = cmd.Payload
	}
	value, err := json.Marshal(map[string]any{
		"request_id":   cmd.RequestID,
		"command_type": cmd.CommandType,
		"payload":      json.RawMessage(payload),
		"sent_at":      time.Now().Unix(),
		"resend":       true,
	})
	if err != nil {
		value = payload
	}

	if err := t.svcCtx.Downlink.Publish(ctx, mqtt.CmdDownTopic(cmd.DeviceID), mqtt.QoSAtLeastOnce, value); err != nil {
		return err
	}
	return t.svcCtx.CommandLogModel.ExtendTimeout(ctx, cmd.RequestID, time.Now().Add(time.Duration(timeoutSec)*time.Second))
}

// resendCount 读取并重发计数; Redis 异常时返回最大值, 直接走超时分支, 不无限重发.
func (t *CommandTimeoutTask) resendCount(requestID string) int64 {
	key := fmt.Sprintf("m1:cmd:resend:%s", requestID)
	n, err := t.svcCtx.Redis.Incr(key)
	if err != nil {
		t.Errorf("重发计数失败, 视为已达上限: requestId=%s, err=%v", requestID, err)
		return int64(t.maxResend) + 1
	}
	if n == 1 {
		if err := t.svcCtx.Redis.Expire(key, resendKeyTTL); err != nil {
			t.Errorf("重发计数设置 TTL 失败: %v", err)
		}
	}
	return n
}
