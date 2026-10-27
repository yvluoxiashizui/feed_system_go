package repos

import (
	"gorm.io/gorm"

	"feed/models"
)

// CreateVideo 创建视频。新视频默认未完成写扩散，由消费者或补扫任务处理
func CreateVideo(v *models.Video) error {
	if v.FanoutDone == nil {
		notDone := false
		v.FanoutDone = &notDone
	}
	return db.Create(v).Error
}

// MarkFanoutDone 标记这条视频的写扩散已经完成，补扫任务不再重投
func MarkFanoutDone(videoID uint) error {
	return db.Model(&models.Video{}).Where("id = ?", videoID).Update("fanout_done", true).Error
}

// ListPendingFanout 查出发布超过指定秒数仍未完成写扩散的视频，用于补投。
// 时间比较交给数据库时钟，避免应用与数据库时区不一致时判定失效。
func ListPendingFanout(delaySeconds int, limit int) ([]models.Video, error) {
	var videos []models.Video
	err := db.Where("fanout_done = ? AND create_time < DATE_SUB(NOW(), INTERVAL ? SECOND)", false, delaySeconds).
		Order("id").Limit(limit).Find(&videos).Error
	return videos, err
}

// ListVideosByCursor 游标分页查视频，最新在前
func ListVideosByCursor(cursor uint, limit int) ([]models.Video, error) {
	var videos []models.Video
	query := db.Order("id DESC").Limit(limit)
	if cursor > 0 {
		query = query.Where("id < ?", cursor)
	}
	if err := query.Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// ListRecentVideoIDs 取最近 N 条视频的 id，按 id 降序
func ListRecentVideoIDs(limit int) ([]uint, error) {
	ids := make([]uint, 0, limit)
	if err := db.Model(&models.Video{}).
		Order("id DESC").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// FindVideoByID 按 ID 查视频
func FindVideoByID(id uint) (*models.Video, error) {
	var v models.Video
	if err := db.Where("id = ?", id).First(&v).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

// IncrementVideoLikes 视频点赞数 +1（原子自增）
func IncrementVideoLikes(videoID uint) error {
	return db.Model(&models.Video{}).
		Where("id = ?", videoID).
		UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error
}

// FindVideosByIDs 按 id 列表查视频
func FindVideosByIDs(ids []uint) ([]models.Video, error) {
	var videos []models.Video
	if err := db.Where("id IN ?", ids).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// ListVideosByLikesCursor 点赞榜查询：点赞数更小，或者点赞数相同但 id 更小。
// 排序字段不唯一，游标必须带上 id 才能保证不重不漏。
func ListVideosByLikesCursor(cursorLikes int64, cursorID uint, limit int) ([]models.Video, error) {
	var videos []models.Video
	query := db.Order("likes_count DESC, id DESC").Limit(limit)
	if cursorID > 0 {
		query = query.Where("likes_count < ? OR (likes_count = ? AND id < ?)",
			cursorLikes, cursorLikes, cursorID)
	}
	if err := query.Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// FindRecentVideoIDsByAuthor 某作者最近的 N 条视频 id，降序
func FindRecentVideoIDsByAuthor(authorID uint, limit int) ([]uint, error) {
	ids := make([]uint, 0, limit)
	if err := db.Model(&models.Video{}).
		Where("author_id = ?", authorID).
		Order("id DESC").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// FindVideoIDsByAuthor 某作者的全部视频 id
func FindVideoIDsByAuthor(authorID uint) ([]uint, error) {
	ids := make([]uint, 0, 64)
	if err := db.Model(&models.Video{}).
		Where("author_id = ?", authorID).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// FindVideosByAuthorIDs 按作者 id 列表查视频，最新在前，游标是视频 id
func FindVideosByAuthorIDs(authorIDs []uint, cursor uint, limit int) ([]models.Video, error) {
	var videos []models.Video
	query := db.Where("author_id IN ?", authorIDs).Order("id DESC").Limit(limit)
	if cursor > 0 {
		query = query.Where("id < ?", cursor)
	}
	if err := query.Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// FindVideoByAuthorAndIdemKey 按幂等键找已经创建过的视频，重复提交时直接返回它
func FindVideoByAuthorAndIdemKey(authorID uint, idemKey string) (*models.Video, error) {
	var v models.Video
	err := db.Where("author_id = ? AND idem_key = ?", authorID, idemKey).First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}
