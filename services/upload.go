package services

import (
	"crypto/rand"
	"encoding/hex"
	"mime/multipart"
	"os"
	"path/filepath"
)

// UploadDir 视频文件的落盘目录，可用环境变量覆盖
func UploadDir() string {
	if v := os.Getenv("FEED_UPLOAD_DIR"); v != "" {
		return v
	}
	return "uploads"
}

// SaveVideo 保存单个视频文件，返回可访问的相对路径。
// 只做单文件直传：分片、断点续传和转码属于另一条链路，不在这里实现。
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
