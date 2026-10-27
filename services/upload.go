package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// 分片上传的约定：前端按 5MB 切片，用整个文件的 MD5 作为本次上传的标识。
// 已传分片记在 Redis 的 Set 里，续传时前端先问一次；合并用 SETNX 加锁保证幂等。
const (
	uploadChunkTTL   = 24 * time.Hour
	uploadLockTTL    = 30 * time.Second
	maxUploadChunks  = 10000
	uploadChunkDirNm = "chunks"
)

// UploadDir 视频文件的落盘目录，可用环境变量覆盖
func UploadDir() string {
	if v := os.Getenv("FEED_UPLOAD_DIR"); v != "" {
		return v
	}
	return "uploads"
}

// SaveVideo 保存单个视频文件，返回可访问的相对路径。
// 小文件走这里直接落盘；大文件走分片那套接口。
func SaveVideo(file *multipart.FileHeader) (string, error) {
	dir := UploadDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	name, err := randomFileName(filepath.Ext(file.Filename))
	if err != nil {
		return "", err
	}

	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := dst.ReadFrom(src); err != nil {
		os.Remove(filepath.Join(dir, name))
		return "", err
	}
	return "/media/" + name, nil
}

// randomFileName 生成随机文件名，保留扩展名，避免重名和路径注入
func randomFileName(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}

// ---------- 分片上传 ----------

// uploadChunkDir 某个上传任务的分片目录：uploads/chunks/<md5>/
func uploadChunkDir(uploadID string) string {
	return filepath.Join(UploadDir(), uploadChunkDirNm, uploadID)
}

// uploadedChunksKey 已传分片集合的 Redis key
func uploadedChunksKey(uploadID string) string {
	return "upload:" + uploadID + ":chunks"
}

// uploadLockKey 合并锁，防止最后几片同时到达时并发合并
func uploadLockKey(uploadID string) string {
	return "upload:" + uploadID + ":lock"
}

// UploadedChunks 返回已经传成功的分片序号（升序），用于断点续传
func UploadedChunks(uploadID string) ([]int, error) {
	members, err := rdb.SMembers(ctx, uploadedChunksKey(uploadID)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	indexes := make([]int, 0, len(members))
	for _, m := range members {
		if n, err := strconv.Atoi(m); err == nil {
			indexes = append(indexes, n)
		}
	}
	sort.Ints(indexes)
	return indexes, nil
}

// SaveChunk 保存一个分片并记账。重复上传同一片是幂等的：覆盖文件、集合里不重复。
func SaveChunk(uploadID string, index, total int, file *multipart.FileHeader) error {
	if uploadID == "" || len(uploadID) > 64 {
		return errors.New("上传标识不合法")
	}
	if index < 0 || total <= 0 || index >= total || total > maxUploadChunks {
		return errors.New("分片参数不合法")
	}

	dir := uploadChunkDir(uploadID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(filepath.Join(dir, strconv.Itoa(index)))
	if err != nil {
		return err
	}
	if _, err := dst.ReadFrom(src); err != nil {
		dst.Close()
		os.Remove(filepath.Join(dir, strconv.Itoa(index)))
		return err
	}
	dst.Close()

	// 先落盘再记账：记账成功但落盘失败会导致合并时缺片
	pipe := rdb.Pipeline()
	pipe.SAdd(ctx, uploadedChunksKey(uploadID), strconv.Itoa(index))
	pipe.Expire(ctx, uploadedChunksKey(uploadID), uploadChunkTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// CompleteUpload 合并分片，返回可播放地址。
// 并发控制交给一把 SETNX 锁：最后几片几乎同时到达时，只有一个请求真正合并，
// 其余的进来发现文件已存在，直接返回同一个地址。
func CompleteUpload(uploadID string, total int, ext string) (string, error) {
	if total <= 0 || total > maxUploadChunks {
		return "", errors.New("分片总数不合法")
	}
	if ext == "" {
		ext = ".mp4"
	}

	finalPath := filepath.Join(UploadDir(), uploadID+ext)
	playURL := "/media/" + uploadID + ext

	// 已经合并过：直接返回同一个地址，天然幂等
	if _, err := os.Stat(finalPath); err == nil {
		return playURL, nil
	}

	locked, err := rdb.SetNX(ctx, uploadLockKey(uploadID), "1", uploadLockTTL).Result()
	if err != nil {
		return "", err
	}
	if !locked {
		return "", errors.New("这个文件正在合并，稍后再试")
	}
	defer rdb.Del(ctx, uploadLockKey(uploadID))

	indexes, err := UploadedChunks(uploadID)
	if err != nil {
		return "", err
	}
	if len(indexes) != total {
		return "", fmt.Errorf("分片不全：已传 %d，总共 %d", len(indexes), total)
	}

	if err := os.MkdirAll(UploadDir(), 0o755); err != nil {
		return "", err
	}
	dst, err := os.Create(finalPath)
	if err != nil {
		return "", err
	}
	for i := 0; i < total; i++ {
		src, err := os.Open(filepath.Join(uploadChunkDir(uploadID), strconv.Itoa(i)))
		if err != nil {
			dst.Close()
			os.Remove(finalPath)
			return "", fmt.Errorf("缺少第 %d 片", i)
		}
		_, copyErr := io.Copy(dst, src)
		src.Close()
		if copyErr != nil {
			dst.Close()
			os.Remove(finalPath)
			return "", copyErr
		}
	}
	if err := dst.Close(); err != nil {
		return "", err
	}

	// 合并成功后清掉分片和记账，失败也删：留着只会占空间
	os.RemoveAll(uploadChunkDir(uploadID))
	rdb.Del(ctx, uploadedChunksKey(uploadID))
	return playURL, nil
}
