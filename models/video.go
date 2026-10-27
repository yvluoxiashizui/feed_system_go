package models

import "time"

// Video 视频表
type Video struct {
	// 主键同时作为点赞榜联合索引的第二列，用于同分时按 id 倒序定位
	ID          uint      `gorm:"primaryKey;index:idx_likes_count_id,priority:2" json:"id"`
	AuthorID    uint      `gorm:"index;uniqueIndex:uk_author_idem,priority:1" json:"author_id"`
	Username    string    `json:"username"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	PlayURL     string    `json:"play_url"`
	CoverURL    string    `json:"cover_url"`
	CreateTime  time.Time `gorm:"autoCreateTime" json:"create_time"`
	// 点赞榜按 (likes_count, id) 排序，联合索引让这条排序直接走索引，不需要额外排序
	LikesCount int64 `gorm:"default:0;index:idx_likes_count_id,priority:1" json:"likes_count"`
	// FanoutDone 标记写扩散是否已完成：发布时置为否，扇出成功后置为是。
	// 用指针区分「没设置过」与「显式置为未完成」，迁移时已有数据按已完成处理。
	FanoutDone *bool `gorm:"default:1;index" json:"-"`
	// IsLiked 当前请求用户是否点过赞，只在响应里用，不落库
	IsLiked bool `gorm:"-" json:"is_liked"`
	// IdemKey 发布请求的幂等键，同一个作者重复提交时由联合唯一索引挡住；
	// 用指针是为了让不带幂等键的记录存 NULL，不参与唯一性约束。
	IdemKey *string `gorm:"size:64;uniqueIndex:uk_author_idem,priority:2" json:"-"`
}
