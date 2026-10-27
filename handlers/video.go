package handlers

import (
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"feed/services"
)

// PublishVideo 发布视频（需登录）
func PublishVideo(c *gin.Context) {
	uid := currentUID(c)

	var input struct {
		Title   string `json:"title"`
		PlayURL string `json:"play_url"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}

	// 客户端可用 Idempotency-Key 头声明这次请求的幂等键，重复提交返回第一次的结果
	video, err := services.PublishVideo(uint(uid), input.Title, input.PlayURL, c.GetHeader("Idempotency-Key"))
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, video)
}

// Feed 流分页参数边界
const (
	defaultFeedLimit = 10
	maxFeedLimit     = 50
)

// parseLimit 收敛 limit：0 或负数会生成 LIMIT 0 / 无 LIMIT，超限则截断
func parseLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return defaultFeedLimit
	}
	if limit > maxFeedLimit {
		return maxFeedLimit
	}
	return limit
}

// ListVideos Feed 流列表，游标分页
func ListVideos(c *gin.Context) {
	limit := parseLimit(c.DefaultQuery("limit", strconv.Itoa(defaultFeedLimit)))
	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)

	page, err := services.ListFeed(currentUID(c), uint(cursor), limit)
	if err != nil {
		c.JSON(500, gin.H{"error": "获取失败"})
		return
	}
	c.JSON(200, page)
}

// GetVideoDetail 视频详情
func GetVideoDetail(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Query("id"), 10, 64)

	video, err := services.GetVideoDetail(currentUID(c), uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "视频不存在"})
		return
	}
	c.JSON(200, video)
}

// LikeVideo 点赞（需登录）
func LikeVideo(c *gin.Context) {
	uid := currentUID(c)

	var input struct {
		VideoID uint `json:"video_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}

	if err := services.LikeVideo(uint(uid), input.VideoID); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "点赞成功"})
}

// UnlikeVideo 取消点赞（需登录）
func UnlikeVideo(c *gin.Context) {
	uid := currentUID(c)

	var input struct {
		VideoID uint `json:"video_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}

	if err := services.UnlikeVideo(uint(uid), input.VideoID); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "已取消点赞"})
}

// IsVideoLiked 查询是否已点赞（需登录）
func IsVideoLiked(c *gin.Context) {
	uid := currentUID(c)
	videoID, _ := strconv.ParseUint(c.Query("video_id"), 10, 64)

	liked, err := services.IsLiked(uint(uid), uint(videoID))
	if err != nil {
		c.JSON(500, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(200, gin.H{"liked": liked})
}

// ListMyLikedVideos 我赞过的视频（需登录）
func ListMyLikedVideos(c *gin.Context) {
	uid := currentUID(c)
	limit := parseLimit(c.DefaultQuery("limit", strconv.Itoa(defaultFeedLimit)))
	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)

	page, err := services.LikedVideos(uint(uid), uint(cursor), limit)
	if err != nil {
		c.JSON(500, gin.H{"error": "获取失败"})
		return
	}
	c.JSON(200, page)
}

// HotVideos 热榜，最近一小时热度排序，深度有上限
func HotVideos(c *gin.Context) {
	limit := parseLimit(c.DefaultQuery("limit", strconv.Itoa(defaultFeedLimit)))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	videos, err := services.HotRank(offset, limit)
	if err != nil {
		c.JSON(500, gin.H{"error": "获取失败"})
		return
	}
	c.JSON(200, videos)
}

// LikesRank 点赞榜，复合游标分页（游标形如 20_111653）
func LikesRank(c *gin.Context) {
	limit := parseLimit(c.DefaultQuery("limit", strconv.Itoa(defaultFeedLimit)))

	page, err := services.LikesRank(c.Query("cursor"), limit)
	if err != nil {
		c.JSON(500, gin.H{"error": "获取失败"})
		return
	}
	c.JSON(200, page)
}

// 上传限制：单文件 100MB，只接受这几种容器格式
const maxUploadBytes = 100 << 20

// 单片上限，前端切成 5MB，留一点余量
const maxChunkBytes = 6 << 20

var allowedVideoExt = map[string]bool{".mp4": true, ".webm": true, ".mov": true}

// UploadVideo 接收单个视频文件并落盘，返回可直接放进 play_url 的地址。
// 只做单文件直传，分片与断点续传属于另一条链路，这里不掺进来。
func UploadVideo(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "缺少文件"})
		return
	}
	if file.Size > maxUploadBytes {
		c.JSON(400, gin.H{"error": "文件超过 100MB"})
		return
	}
	ext := strings.ToLower(path.Ext(file.Filename))
	if !allowedVideoExt[ext] {
		c.JSON(400, gin.H{"error": "只接受 mp4、webm、mov"})
		return
	}

	url, err := services.SaveVideo(file)
	if err != nil {
		c.JSON(500, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(200, gin.H{"play_url": url})
}

// UploadChunk 接收一个分片。前端按 5MB 切片，用整个文件的 MD5 作为上传标识。
func UploadChunk(c *gin.Context) {
	uploadID := c.PostForm("upload_id")
	index, _ := strconv.Atoi(c.PostForm("index"))
	total, _ := strconv.Atoi(c.PostForm("total"))

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "缺少分片"})
		return
	}
	if file.Size > maxChunkBytes {
		c.JSON(400, gin.H{"error": "分片超过 5MB"})
		return
	}
	if err := services.SaveChunk(uploadID, index, total, file); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	received, _ := services.UploadedChunks(uploadID)
	c.JSON(200, gin.H{"received": len(received)})
}

// UploadStatus 查询已传分片，用于断点续传
func UploadStatus(c *gin.Context) {
	uploadID := c.Query("upload_id")
	if uploadID == "" {
		c.JSON(400, gin.H{"error": "缺少上传标识"})
		return
	}
	received, err := services.UploadedChunks(uploadID)
	if err != nil {
		c.JSON(500, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(200, gin.H{"received": received})
}

// CompleteUpload 合并分片，重复调用返回同一个地址
func CompleteUpload(c *gin.Context) {
	var input struct {
		UploadID string `json:"upload_id"`
		Total    int    `json:"total"`
		Ext      string `json:"ext"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}
	if !allowedVideoExt[strings.ToLower(input.Ext)] {
		input.Ext = ".mp4"
	}
	url, err := services.CompleteUpload(input.UploadID, input.Total, strings.ToLower(input.Ext))
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"play_url": url})
}
