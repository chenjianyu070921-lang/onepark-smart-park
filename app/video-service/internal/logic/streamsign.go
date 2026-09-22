package logic

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// SignAlgorithm 流地址签名算法标识(docs/m3/11 §时效性签名).
// 校验方(网关 / 流媒体 hook)按此算法复算签名。
const SignAlgorithm = "HMAC-SHA256"

// QueryExpires / QuerySign 是签名参数在拉流地址上的查询串键名。
// 形如 http://media/live/cam-1.flv?expires=1730000000&sign=ab12...
const (
	QueryExpires = "expires"
	QuerySign    = "sign"
)

// streamSignPayload 拼接参与签名的原文。
//
// 只绑定「摄像头ID + 过期时间」两个字段:
//   - camera_id 决定"这个签名能给哪台设备拉流", 签名被复制到别的摄像头地址上直接失效;
//   - expires 决定"这个签名有效到什么时候", 过期后即使签名正确也拒绝。
//
// 刻意不把 RTSP 地址(可能含明文账号密码)写进签名原文: 原文一旦需要参与校验,
// 就会被迫在 URL 上回传凭据, 反而放大泄漏面。
func streamSignPayload(cameraID, expiresAt int64) string {
	return strconv.FormatInt(cameraID, 10) + "|" + strconv.FormatInt(expiresAt, 10)
}

// SignStream 生成流地址签名。
//
// secret 为空(未配置密钥)时返回空串, 调用方据此判断"未启用签名",
// 从而让"未配置=不签名"这条向后兼容语义只有一处判断。
func SignStream(secret string, cameraID, expiresAt int64) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(streamSignPayload(cameraID, expiresAt)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyStreamSign 校验流地址签名, 供网关 / 流媒体服务拉流前鉴权使用。
//
// 三类拒绝: 未配置密钥 / 签名缺失 / 已过期(或签名不匹配)。
// 用 hmac.Equal 而非 == 比较: 字符串比较会在首个不同字节提前返回,
// 理论上可被用来逐字节爆破签名。
func VerifyStreamSign(secret string, cameraID, expiresAt int64, sign string, now time.Time) bool {
	if strings.TrimSpace(secret) == "" || strings.TrimSpace(sign) == "" {
		return false
	}
	if now.Unix() > expiresAt {
		return false
	}
	return hmac.Equal([]byte(SignStream(secret, cameraID, expiresAt)), []byte(sign))
}

// playbackSignPayload 回放签名原文: 绑定「摄像头ID + 时间范围 + 过期时间」。
//
// 为什么回放不能复用拉流签名: 拉流签名只回答"能不能看这个摄像头",
// 回放多一个"能看哪一段"的维度 —— 若沿用只绑 camera_id+expires 的签名,
// 一个合法签名可以被拿到同一摄像头的任意时间段上重放, 时段授权形同虚设。
func playbackSignPayload(cameraID, startUnix, endUnix, expiresAt int64) string {
	return strconv.FormatInt(cameraID, 10) + "|" + strconv.FormatInt(startUnix, 10) + "|" +
		strconv.FormatInt(endUnix, 10) + "|" + strconv.FormatInt(expiresAt, 10)
}

// SignPlayback 生成回放地址签名; 未配置密钥时返回空串(与拉流签名同一套取舍)。
func SignPlayback(secret string, cameraID, startUnix, endUnix, expiresAt int64) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(playbackSignPayload(cameraID, startUnix, endUnix, expiresAt)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyPlaybackSign 校验回放签名(网关 / 流媒体服务回放前鉴权)。
func VerifyPlaybackSign(secret string, cameraID, startUnix, endUnix, expiresAt int64, sign string, now time.Time) bool {
	if strings.TrimSpace(secret) == "" || strings.TrimSpace(sign) == "" {
		return false
	}
	if now.Unix() > expiresAt {
		return false
	}
	return hmac.Equal([]byte(SignPlayback(secret, cameraID, startUnix, endUnix, expiresAt)), []byte(sign))
}

// signedFlvURL 给 FLV 拉流地址追加时效签名参数。
//
// base 为空(未配置流媒体服务)或未启用签名时原样返回: 不编造带参数的空地址,
// 也不在未配置密钥时留下 expires 这个"看起来已鉴权"的假象。
func signedFlvURL(base string, cameraID, expiresAt int64, secret string) string {
	if base == "" {
		return ""
	}
	sign := SignStream(secret, cameraID, expiresAt)
	if sign == "" {
		return base
	}
	return base + "?" + QueryExpires + "=" + strconv.FormatInt(expiresAt, 10) +
		"&" + QuerySign + "=" + sign
}
