package services

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectStore 视频文件的存放位置。
// 默认落本地磁盘；配置了 S3 兼容的地址与密钥之后走对象存储。
type ObjectStore interface {
	// Put 保存一个对象，返回可以直接放进 play_url 的地址
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error)
}

var store ObjectStore = diskStore{}

// Storage 返回当前的存储实现
func Storage() ObjectStore { return store }

// InitStorage 按环境变量初始化存储：配了对象存储就用对象存储，否则落本地磁盘
func InitStorage() error {
	endpoint := os.Getenv("FEED_S3_ENDPOINT")
	if endpoint == "" {
		store = diskStore{}
		return nil
	}

	accessKey := os.Getenv("FEED_S3_ACCESS_KEY")
	secretKey := os.Getenv("FEED_S3_SECRET_KEY")
	bucket := os.Getenv("FEED_S3_BUCKET")
	if bucket == "" {
		bucket = "feed"
	}
	publicBase := os.Getenv("FEED_S3_PUBLIC_URL")

	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	secure := strings.HasPrefix(endpoint, "https://")
	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		return err
	}

	ctx := context.Background()
	ok, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !ok {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return err
		}
	}
	// 视频要能被浏览器直接播放，所以桶开只读公开策略
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, bucket)
	if err := client.SetBucketPolicy(ctx, bucket, policy); err != nil {
		return err
	}

	if publicBase == "" {
		publicBase = strings.TrimRight(endpoint, "/")
	}
	store = &s3Store{client: client, bucket: bucket, publicBase: strings.TrimRight(publicBase, "/")}
	return nil
}

// diskStore 落本地磁盘，由 /media 路径对外提供访问
type diskStore struct{}

func (diskStore) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) (string, error) {
	dir := UploadDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst, err := os.Create(filepath.Join(dir, key))
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, r); err != nil {
		os.Remove(filepath.Join(dir, key))
		return "", err
	}
	return "/media/" + key, nil
}

// s3Store 走 S3 兼容的对象存储（MinIO、OSS、S3 都行）
type s3Store struct {
	client     *minio.Client
	bucket     string
	publicBase string
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", err
	}
	return s.publicBase + "/" + s.bucket + "/" + key, nil
}
