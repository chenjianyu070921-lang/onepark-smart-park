package frame

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"onepark/app/gateway-service/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
)

// Handler 单条 TCP 连接的传输适配层: 拥有 *Session, 负责按行读取 JSON 帧并写回响应.
// 业务语义全部在 Session.HandleFrame 中, 本层只管连接生命周期与编解码(与 CoAP 适配层对称).
type Handler struct {
	logx.Logger
	svcCtx *svc.ServiceContext
}

func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{Logger: logx.WithContext(context.Background()), svcCtx: svcCtx}
}

// Serve 处理一条 TCP 连接, 直到对端关闭/认证超时/协议错误.
func (h *Handler) Serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	sess := NewSession(h.svcCtx)
	sess.SetRemote(remote)

	reader := bufio.NewReader(conn)
	cfg := h.svcCtx.Config

	maxBytes := cfg.MaxFrameBytes
	if maxBytes <= 0 {
		maxBytes = 8192
	}
	readTimeout := time.Duration(cfg.ReadTimeoutSec) * time.Second
	if readTimeout <= 0 {
		readTimeout = 120 * time.Second
	}
	authTimeout := time.Duration(cfg.AuthTimeoutSec) * time.Second
	if authTimeout <= 0 {
		authTimeout = 10 * time.Second
	}

	// 建连后必须在认证时限内完成认证
	if err := conn.SetReadDeadline(time.Now().Add(authTimeout)); err != nil {
		h.Errorf("设置读超时失败: %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := Read(reader, maxBytes)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				h.Infof("连接空闲超时, 断开: remote=%s", remote)
			} else if !errors.Is(err, net.ErrClosed) && err.Error() != "EOF" {
				h.Errorf("读取帧失败: remote=%s, err=%v", remote, err)
			}
			return
		}
		if len(line) == 0 {
			continue
		}

		var f Frame
		if err := json.Unmarshal(line, &f); err != nil {
			h.Errorf("帧解析失败, 断开: remote=%s, err=%v", remote, err)
			writeFrame(conn, Response{OK: false, Error: "帧格式非法"})
			return
		}

		resp, keep := sess.HandleFrame(ctx, &f)
		writeFrame(conn, resp)
		if !keep {
			return
		}

		// 认证通过后放宽为普通读超时, 设备以 ping 保活
		if sess.authed {
			if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				h.Errorf("设置读超时失败: %v", err)
				return
			}
		}
	}
}

// writeFrame 序列化响应并写回 TCP 连接(末尾补 \n 与 TCP 帧格式一致).
func writeFrame(conn net.Conn, resp Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if _, err := conn.Write(b); err != nil {
		// 连接已断, 上层循环会在下次 Read 时感知, 此处无需处理
		_ = err
	}
}
