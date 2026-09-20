package coap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	piondtls "github.com/pion/dtls/v3"
	"github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/responsewriter"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
	udpClient "github.com/plgd-dev/go-coap/v3/udp/client"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/gateway-service/internal/frame"
	"onepark/app/gateway-service/internal/svc"
)

// Handler 处理单个 CoAP 请求: 解析 JSON 帧, 复用 frame.Session 业务语义(与 TCP 完全一致).
//
// CoAP 运行在 UDP 上、无连接概念, Session 为每请求实例(TCP 下为每连接实例); 二者都收敛到
// Session.HandleFrame. 认证沿用 frame 既有 bcrypt(device_id+secret)(设计文档 §4.1 选项 A):
// 设计文档方案 C 意图在 DTLS 模式跳过应用层认证, 但 pion DTLS PSK 服务端为单一共享密钥,
// 无法把 DTLS 握手身份绑定到具体设备, 故 DTLS 模式仍保留每请求 bcrypt 以保证每设备正确鉴权;
// 待 pion 支持按身份 PSK 或改用证书(RawPublicKey)后再优化为方案 C.
func Handler(svcCtx *svc.ServiceContext) udpClient.HandlerFunc {
	return func(w *responsewriter.ResponseWriter[*udpClient.Conn], r *pool.Message) {
		body, err := io.ReadAll(r.Body())
		if err != nil {
			writeErr(w, "读取请求体失败")
			return
		}
		var f frame.Frame
		if err := json.Unmarshal(body, &f); err != nil {
			writeErr(w, "帧格式非法")
			return
		}

		sess := frame.NewSession(svcCtx)
		sess.SetSource("coap-gateway")
		if ra := w.Conn().RemoteAddr(); ra != nil {
			sess.SetRemote(ra.String())
		}

		// CoAP 无连接: 每请求携带 device_id+secret, 先认证再按类型分发(见 frame.Session.HandleStateless).
		resp, _ := sess.HandleStateless(r.Context(), &f)
		writeResp(w, resp)
	}
}

func writeResp(w *responsewriter.ResponseWriter[*udpClient.Conn], resp frame.Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		writeErr(w, "序列化响应失败")
		return
	}
	code := codes.Content
	if !resp.OK {
		code = codes.BadRequest
	}
	if err := w.SetResponse(code, message.AppJSON, bytes.NewReader(b)); err != nil {
		logx.Errorf("coap 写响应失败: %v", err)
	}
}

func writeErr(w *responsewriter.ResponseWriter[*udpClient.Conn], msg string) {
	b, _ := json.Marshal(frame.Response{OK: false, Error: msg})
	_ = w.SetResponse(codes.BadRequest, message.AppJSON, bytes.NewReader(b))
}

// Start 启动 CoAP 接入(仅当 CoAP.Enabled). 可同时起 NoSec UDP(5683) 与 DTLS(5684).
// 各 listener 在独立 goroutine 中 Serve, 随 ctx 取消而停止; 启动失败返回 error.
func Start(ctx context.Context, svcCtx *svc.ServiceContext) error {
	cfg := svcCtx.Config.CoAP
	if !cfg.Enabled {
		logx.Info("coap 未启用(CoAP.Enabled=false), 跳过启动")
		return nil
	}
	h := Handler(svcCtx)

	if cfg.DTLSEnabled {
		if err := startDTLS(ctx, svcCtx, h); err != nil {
			return err
		}
	}

	// NoSec UDP: 始终可用(内网/联调). 生产建议仅开 DTLS, 由防火墙限制 5683 暴露面.
	laddr := fmt.Sprintf("%s:%d", orDefault(cfg.Host, "0.0.0.0"), orDefaultPort(cfg.Port, 5683))
	l, err := net.NewListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("coap udp 监听失败 %s: %w", laddr, err)
	}
	srv := udp.NewServer(options.WithHandlerFunc(h))
	go func() {
		<-ctx.Done()
		srv.Stop()
		_ = l.Close()
	}()
	logx.Infof("gateway-service coap(noSec) listening on %s", laddr)
	go func() {
		if err := srv.Serve(l); err != nil && ctx.Err() == nil {
			logx.Errorf("coap udp serve 异常退出: %v", err)
		}
	}()
	return nil
}

func startDTLS(ctx context.Context, svcCtx *svc.ServiceContext, h udpClient.HandlerFunc) error {
	cfg := svcCtx.Config.CoAP
	if cfg.DTLSPSK == "" {
		return fmt.Errorf("coap DTLS 已启用但 CoAP.DTLSPSK 为空: 拒绝启动(安全兜底)")
	}
	psk := []byte(cfg.DTLSPSK)
	dtlsCfg := &piondtls.Config{
		PSK: func(hint []byte) ([]byte, error) {
			return psk, nil
		},
		PSKIdentityHint: []byte("onepark-gateway"),
		CipherSuites:    []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8},
	}
	laddr := fmt.Sprintf("%s:%d", orDefault(cfg.Host, "0.0.0.0"), orDefaultPort(cfg.DTLSPort, 5684))
	l, err := net.NewDTLSListener("udp", laddr, dtlsCfg)
	if err != nil {
		return fmt.Errorf("coap dtls 监听失败 %s: %w", laddr, err)
	}
	srv := dtls.NewServer(options.WithHandlerFunc(h))
	go func() {
		<-ctx.Done()
		srv.Stop()
		_ = l.Close()
	}()
	logx.Infof("gateway-service coap(dtls) listening on %s", laddr)
	go func() {
		if err := srv.Serve(l); err != nil && ctx.Err() == nil {
			logx.Errorf("coap dtls serve 异常退出: %v", err)
		}
	}()
	return nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func orDefaultPort(p, def int) int {
	if p <= 0 {
		return def
	}
	return p
}
