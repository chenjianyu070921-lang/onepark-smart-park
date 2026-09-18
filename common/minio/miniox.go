// Package miniox 提供 MinIO(S3 兼容对象存储) 客户端初始化封装, 供各业务服务复用.
// 与 gormx/redisx 保持一致的"别名 + New"封装风格, 业务层不直接依赖 minio-go 版本.
package miniox

import (
	"context"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinIOConf 定义 MinIO 连接配置, 与 go-zero 配置加载保持一致.
type MinIOConf struct {
	Endpoint      string `json:",optional"`          // 如 127.0.0.1:9000
	AccessKey     string `json:",optional"`          // AccessKey
	SecretKey     string `json:",optional"`          // SecretKey
	Bucket        string `json:",default=workorder"` // 默认桶名
	UseSSL        bool   `json:",default=false"`     // 是否启用 HTTPS
	PublicBaseURL string `json:",optional"`          // 对象公网基础地址(可选); 为空时前端需走预签名下载
}

// Client 是 *minio.Client 的别名, 避免各服务直接依赖 minio-go 版本.
type Client = minio.Client

// NewClient 根据配置创建 MinIO 客户端(惰性, 不会立即建立 TCP 连接).
func NewClient(c MinIOConf) (*minio.Client, error) {
	return minio.New(c.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: c.UseSSL,
	})
}

// EnsureBucket 确保桶存在(不存在则创建); 已存在时忽略错误.
func EnsureBucket(ctx context.Context, client *minio.Client, bucket string) error {
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
}

// PutObject 上传对象到 MinIO, 封装 PutObjectOptions, 避免业务层直接依赖 minio-go 版本.
func PutObject(ctx context.Context, client *minio.Client, bucket, object string, data io.Reader, size int64, contentType string) (minio.UploadInfo, error) {
	return client.PutObject(ctx, bucket, object, data, size, minio.PutObjectOptions{ContentType: contentType})
}
