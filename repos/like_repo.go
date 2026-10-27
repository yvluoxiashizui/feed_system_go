package repos

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"feed/models"
)

// isDuplicateKey 判断是否为唯一键冲突（MySQL 1062）
func isDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// LikeAndIncrement 在一个事务内插入点赞记录并累加计数。
// duplicated 为 true 表示唯一索引冲突，即这个用户已经点过赞。
func LikeAndIncrement(userID, videoID uint) (duplicated bool, err error) {
	err = db.Transaction(func(tx *gorm.DB) error {
		like := &models.Like{UserID: userID, VideoID: videoID}
		if e := tx.Create(like).Error; e != nil {
			if isDuplicateKey(e) {
				duplicated = true
				return nil
			}
			return e
		}
		return tx.Model(&models.Video{}).
			Where("id = ?", videoID).
			UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error
	})
	return duplicated, err
}

// UnlikeAndDecrement 在一个事务内删除点赞记录并回退计数。
// 返回 removed 表示确实删掉了一条记录；记录不存在时不动计数，
// 避免重复取消把点赞数减成负数。
func UnlikeAndDecrement(userID, videoID uint) (bool, error) {
	removed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("user_id = ? AND video_id = ?", userID, videoID).Delete(&models.Like{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		removed = true
		// 条件里带 likes_count > 0 兜底，历史脏数据也不会被减成负数
		return tx.Model(&models.Video{}).
			Where("id = ? AND likes_count > 0", videoID).
			UpdateColumn("likes_count", gorm.Expr("likes_count - 1")).Error
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// IsLiked 判断用户是否已点赞
func IsLiked(userID, videoID uint) (bool, error) {
	var count int64
	if err := db.Model(&models.Like{}).
		Where("user_id = ? AND video_id = ?", userID, videoID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListLikedVideoIDs 按点赞记录 id 倒序取用户赞过的视频 id，并返回下一页游标
func ListLikedVideoIDs(userID, cursor uint, limit int) ([]uint, int, error) {
	var likes []models.Like
	query := db.Where("user_id = ?", userID).Order("id DESC").Limit(limit)
	if cursor > 0 {
		query = query.Where("id < ?", cursor)
	}
	if err := query.Find(&likes).Error; err != nil {
		return nil, 0, err
	}

	ids := make([]uint, 0, len(likes))
	for _, l := range likes {
		ids = append(ids, l.VideoID)
	}
	next := 0
	if len(likes) > 0 {
		next = int(likes[len(likes)-1].ID)
	}
	return ids, next, nil
}

// LikedAmong 返回这批视频里该用户点过赞的 id 集合，一次查询覆盖整页
func LikedAmong(userID uint, videoIDs []uint) (map[uint]bool, error) {
	liked := make(map[uint]bool, len(videoIDs))
	if userID == 0 || len(videoIDs) == 0 {
		return liked, nil
	}
	var ids []uint
	if err := db.Model(&models.Like{}).
		Where("user_id = ? AND video_id IN ?", userID, videoIDs).
		Pluck("video_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		liked[id] = true
	}
	return liked, nil
}
