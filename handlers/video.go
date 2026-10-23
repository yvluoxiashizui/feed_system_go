package handlers

import (
	"strconv"

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

	video, err := services.PublishVideo(uint(uid), input.Title, input.PlayURL)
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

	page, err := services.ListFeed(uint(cursor), limit)
	if err != nil {
		c.JSON(500, gin.H{"error": "获取失败"})
		return
	}
	c.JSON(200, page)
}

// GetVideoDetail 视频详情
func GetVideoDetail(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Query("id"), 10, 64)

	video, err := services.GetVideoDetail(uint(id))
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
