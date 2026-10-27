package services

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// 用内存版 S3 验证对象存储那条路径：建桶、整份上传、取回来比对内容
func TestS3StorePut(t *testing.T) {
	backend := s3mem.New()
	server := httptest.NewServer(gofakes3.New(backend).Server())
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4("key", "secret", ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("建客户端失败: %v", err)
	}

	ctx := context.Background()
	if err := client.MakeBucket(ctx, "feed", minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("建桶失败: %v", err)
	}

	store := &s3Store{client: client, bucket: "feed", publicBase: "http://cdn.example.com"}
	content := []byte("hello feed video bytes")

	url, err := store.Put(ctx, "abc.mp4", bytes.NewReader(content), int64(len(content)), "video/mp4")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if url != "http://cdn.example.com/feed/abc.mp4" {
		t.Fatalf("地址不对: %s", url)
	}

	obj, err := client.GetObject(ctx, "feed", "abc.mp4", minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("取对象失败: %v", err)
	}
	defer obj.Close()
	got, err := io.ReadAll(obj)
	if err != nil {
		t.Fatalf("读对象失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("内容不一致: %q", got)
	}
}
