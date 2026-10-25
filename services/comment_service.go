package services

import (
	"errors"
	"strings"
	"unicode/utf8"

	"feed/models"
	"feed/repos"
)

// maxCommentLength 单条评论的最大字符数
const maxCommentLength = 500

// PublishComment 发表评论：内容不能为空或过长，目标视频必须存在
func PublishComment(userID, videoID uint, content string) (*models.Comment, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("评论内容不能为空")
	}
	if utf8.RuneCountInString(content) > maxCommentLength {
		return nil, errors.New("评论最多 500 字")
	}
	if _, err := repos.FindVideoByID(videoID); err != nil {
		return nil, errors.New("视频不存在")
	}

	author, err := repos.FindUserByID(userID)
	if err != nil {
		return nil, errors.New("用户不存在")
	}
	c := &models.Comment{UserID: userID, Username: author.Username, VideoID: videoID, Content: content}
	if err := repos.CreateComment(c); err != nil {
		return nil, err
	}

	// 评论和点赞一样计入热度：有争议的视频往往评论多、点赞少，只看点赞会漏掉这类内容
	hotIncr(videoID, 1)
	return c, nil
}

// commentPage 评论分页数据
type commentPage struct {
	Comments   []models.Comment `json:"comments"`
	NextCursor int              `json:"next_cursor"`
}

// ListComments 评论列表，游标是评论 id
func ListComments(videoID, cursor uint, limit int) (commentPage, error) {
	comments, err := repos.ListCommentsByVideo(videoID, cursor, limit)
	if err != nil {
		return commentPage{}, err
	}
	page := commentPage{Comments: comments}
	if len(comments) > 0 {
		page.NextCursor = int(comments[len(comments)-1].ID)
	}
	return page, nil
}
