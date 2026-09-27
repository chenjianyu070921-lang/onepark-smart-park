package logic

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testSignSecret = "onepark-video-sign-secret"

// TestSignStream_DisabledWithoutSecret 未配置密钥必须返回空签名 ——
// 这是调用方判断"本部署是否启用签名"的唯一信号, 不能返回一个"看起来有效"的值.
func TestSignStream_DisabledWithoutSecret(t *testing.T) {
	if got := SignStream("", 1, 100); got != "" {
		t.Errorf("未配置密钥应返回空签名, 实际 %q", got)
	}
	if got := SignStream("   ", 1, 100); got != "" {
		t.Errorf("全空白密钥应视为未配置, 实际 %q", got)
	}
}

// TestSignStream_StableAndSensitive 签名必须确定, 且对三个输入维度都敏感.
// 任一维度不影响签名, 都意味着签名可以被搬用到别的摄像头/无限延期.
func TestSignStream_StableAndSensitive(t *testing.T) {
	base := SignStream(testSignSecret, 1001, 1700000000)
	if len(base) != 64 {
		t.Fatalf("sha256 十六进制应为 64 位, 实际 %d 位: %s", len(base), base)
	}
	if again := SignStream(testSignSecret, 1001, 1700000000); again != base {
		t.Error("相同输入应产生相同签名")
	}
	if SignStream(testSignSecret, 1002, 1700000000) == base {
		t.Error("camera_id 变化必须改变签名")
	}
	if SignStream(testSignSecret, 1001, 1700000001) == base {
		t.Error("expires 变化必须改变签名")
	}
	if SignStream(testSignSecret+"x", 1001, 1700000000) == base {
		t.Error("密钥变化必须改变签名")
	}
}

// TestVerifyStreamSign 覆盖校验的全部拒绝分支与到期边界.
func TestVerifyStreamSign(t *testing.T) {
	now := time.Unix(1700000000, 0)
	expires := now.Add(time.Hour).Unix()
	valid := SignStream(testSignSecret, 1001, expires)
	expiredAt := now.Add(-time.Second).Unix()

	cases := []struct {
		name    string
		secret  string
		camera  int64
		expires int64
		sign    string
		want    bool
	}{
		{"正确签名放行", testSignSecret, 1001, expires, valid, true},
		{"到期当刻仍有效", testSignSecret, 1001, now.Unix(), SignStream(testSignSecret, 1001, now.Unix()), true},
		{"已过期拒绝", testSignSecret, 1001, expiredAt, SignStream(testSignSecret, 1001, expiredAt), false},
		{"换成别的摄像头拒绝", testSignSecret, 1002, expires, valid, false},
		{"篡改过期时间拒绝", testSignSecret, 1001, expires + 3600, valid, false},
		{"未配置密钥拒绝", "", 1001, expires, valid, false},
		{"签名缺失拒绝", testSignSecret, 1001, expires, "", false},
		{"签名错误拒绝", testSignSecret, 1001, expires, "deadbeef", false},
		{"密钥不符拒绝", "another-secret", 1001, expires, valid, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VerifyStreamSign(c.secret, c.camera, c.expires, c.sign, now); got != c.want {
				t.Errorf("VerifyStreamSign() = %v, 期望 %v", got, c.want)
			}
		})
	}
}

// TestSignedFlvURL 校验 FLV 地址的三种形态: 无基地址 / 未启用签名 / 启用签名.
func TestSignedFlvURL(t *testing.T) {
	const base = "http://media/live/cam-1.flv"
	expires := time.Now().Add(time.Hour).Unix()

	if got := signedFlvURL("", 1001, expires, testSignSecret); got != "" {
		t.Errorf("未配置流媒体服务时应返回空串, 实际 %q", got)
	}
	// 未启用签名时不能留下 expires 参数: 那会让调用方误以为地址已经鉴权过.
	if got := signedFlvURL(base, 1001, expires, ""); got != base {
		t.Errorf("未启用签名应原样返回, 实际 %q", got)
	}

	signed := signedFlvURL(base, 1001, expires, testSignSecret)
	if !strings.HasPrefix(signed, base+"?"+QueryExpires+"=") {
		t.Fatalf("签名地址前缀异常: %s", signed)
	}
	query := queryOf(t, signed)
	if query[QueryExpires] != strconv.FormatInt(expires, 10) {
		t.Errorf("expires 参数不一致: %q", query[QueryExpires])
	}
	// 端到端: 从生成到校验整条链路必须自洽(参数名/编码/算法任一环节错位都会在这里暴露).
	if !VerifyStreamSign(testSignSecret, 1001, expires, query[QuerySign], time.Now()) {
		t.Errorf("FLV 地址上的签名应可通过校验, 实际 sign=%q", query[QuerySign])
	}
}

// queryOf 解析地址查询串为单值映射, 仅测试使用.
func queryOf(t *testing.T, raw string) map[string]string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("地址不可解析: %v", err)
	}
	out := make(map[string]string, len(u.Query()))
	for k, v := range u.Query() {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
