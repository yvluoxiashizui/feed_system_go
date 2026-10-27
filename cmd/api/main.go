package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"feed/bootstrap"
	"feed/handlers"
	"feed/models"
	"feed/services"
)

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

	// Feed 预览页
	r.StaticFile("/", "preview.html")

	if err := r.Run(bootstrap.Env("FEED_HTTP_ADDR", ":8080")); err != nil {
		log.Fatal("启动服务失败: ", err)
	}
}
