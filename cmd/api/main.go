package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"feed/bootstrap"
	"feed/handlers"
	"feed/models"
	"feed/services"
)

// shutdownTimeout 停机时等待在途请求的最长时间
const shutdownTimeout = 5 * time.Second

func main() {
	db := bootstrap.OpenDB()
	db.AutoMigrate(&models.User{}, &models.Video{}, &models.Like{}, &models.Follow{}, &models.Comment{})

	bootstrap.OpenRedis()

	// 写扩散由独立 Worker 消费；投递侧连不上时退回同步扇出，API 照常启动
	if err := services.SetupFanoutPublisher(); err != nil {
		log.Println("消息队列不可用，写扩散退回同步执行:", err)
	} else {
		log.Println("写扩散已投递到消息队列")
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(handlers.Logger)
	r.POST("/register", handlers.RateLimit("register", handlers.RegisterRate, handlers.RegisterBurst), handlers.Register)
	r.POST("/login", handlers.RateLimit("login", handlers.LoginRate, handlers.LoginBurst), handlers.Login)
	r.GET("/healthz", handlers.Health)
	r.GET("/user", handlers.GetUser)
	r.GET("/auth-test", handlers.Auth, func(c *gin.Context) {
		userID := c.GetString("user_id")
		c.JSON(200, gin.H{"message": "已登录", "user_id": userID})
	})
	r.POST("/video/upload", handlers.Auth, handlers.RateLimit("upload", handlers.PublishRate, handlers.PublishBurst), handlers.UploadVideo)
	r.POST("/video/upload/chunk", handlers.Auth, handlers.UploadChunk)
	r.GET("/video/upload/status", handlers.Auth, handlers.UploadStatus)
	r.POST("/video/upload/complete", handlers.Auth, handlers.CompleteUpload)
	r.POST("/video/publish", handlers.Auth, handlers.RateLimit("publish", handlers.PublishRate, handlers.PublishBurst), handlers.PublishVideo)
	r.GET("/videos", handlers.SoftAuth, handlers.ListVideos)
	r.GET("/video/detail", handlers.SoftAuth, handlers.GetVideoDetail)
	r.POST("/video/like", handlers.Auth, handlers.LikeVideo)
	r.POST("/video/unlike", handlers.Auth, handlers.UnlikeVideo)
	r.GET("/video/isLiked", handlers.Auth, handlers.IsVideoLiked)
	r.GET("/video/liked", handlers.Auth, handlers.ListMyLikedVideos)
	r.POST("/social/follow", handlers.Auth, handlers.FollowUser)
	r.POST("/social/unfollow", handlers.Auth, handlers.Unfollow)
	r.GET("/social/isFollowing", handlers.Auth, handlers.IsFollowing)
	r.GET("/feed/following", handlers.Auth, handlers.FollowingFeed)
	r.GET("/feed/hot", handlers.HotVideos)
	r.GET("/feed/likes-rank", handlers.LikesRank)
	r.POST("/comment/publish", handlers.Auth, handlers.PublishComment)
	r.GET("/comment/list", handlers.ListComments)

	// 存储层：配了对象存储就走对象存储，否则落本地磁盘
	if err := services.InitStorage(); err != nil {
		log.Println("对象存储初始化失败，退回本地磁盘:", err)
	}

	// 演示页与上传的视频文件；上传目录可用 FEED_UPLOAD_DIR 覆盖
	uploadDir := services.UploadDir()
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		log.Println("创建上传目录失败:", err)
	}
	r.Static("/media", uploadDir)
	r.StaticFile("/", "preview.html")

	srv := &http.Server{
		Addr:    bootstrap.Env("FEED_HTTP_ADDR", ":8080"),
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("启动服务失败: ", err)
		}
	}()
	log.Println("服务已启动:", srv.Addr)

	// 收到退出信号后先停止接收新请求，再等在途请求结束
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("收到退出信号，停止接收新请求")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Println("等待在途请求超时:", err)
	}
	log.Println("服务已退出")
}
