package repos

import "feed/models"

// CreateFollow 创建关注关系，重复会因唯一索引报错
func CreateFollow(f *models.Follow) error {
	return db.Create(f).Error
}

// DeleteFollow 删除关注关系，返回是否真的删掉了一条
func DeleteFollow(followerID, vloggerID uint) (bool, error) {
	res := db.Where("follower_id = ? AND vlogger_id = ?", followerID, vloggerID).
		Delete(&models.Follow{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// IsFollowing 判断是否已关注
func IsFollowing(followerID, vloggerID uint) (bool, error) {
	var count int64
	if err := db.Model(&models.Follow{}).
		Where("follower_id = ? AND vlogger_id = ?", followerID, vloggerID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountFollowers 某作者的粉丝数
func CountFollowers(vloggerID uint) (int64, error) {
	var count int64
	err := db.Model(&models.Follow{}).Where("vlogger_id = ?", vloggerID).Count(&count).Error
	return count, err
}

// CountFollowersByIDs 批量统计多位作者的粉丝数，没有粉丝的不出现在结果里
func CountFollowersByIDs(vloggerIDs []uint) (map[uint]int64, error) {
	type row struct {
		VloggerID uint
		Total     int64
	}
	var rows []row
	err := db.Model(&models.Follow{}).
		Select("vlogger_id, count(*) as total").
		Where("vlogger_id IN ?", vloggerIDs).
		Group("vlogger_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.VloggerID] = r.Total
	}
	return out, nil
}

// FindFollowerIDs 某作者的所有粉丝 id
func FindFollowerIDs(vloggerID uint) ([]uint, error) {
	ids := make([]uint, 0, 64)
	if err := db.Model(&models.Follow{}).
		Where("vlogger_id = ?", vloggerID).
		Pluck("follower_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// FindFollowingVloggerIDs 查出当前用户关注的所有博主 id
func FindFollowingVloggerIDs(followerID uint) ([]uint, error) {
	var follows []models.Follow
	if err := db.Where("follower_id = ?", followerID).Find(&follows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(follows))
	for _, f := range follows {
		ids = append(ids, f.VloggerID)
	}
	return ids, nil
}

// FollowingWithFollowerCounts 一次查出关注列表和每个作者的粉丝数。
// 合成一次查询是为了让关注流读取少一次往返：这两份数据都来自 follows 表。
func FollowingWithFollowerCounts(followerID uint) ([]uint, map[uint]int64, error) {
	type row struct {
		VloggerID uint
		Total     int64
	}
	var rows []row
	err := db.Table("follows AS f").
		Select("f.vlogger_id AS vlogger_id, (SELECT COUNT(*) FROM follows f2 WHERE f2.vlogger_id = f.vlogger_id) AS total").
		Where("f.follower_id = ?", followerID).
		Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}

	ids := make([]uint, 0, len(rows))
	counts := make(map[uint]int64, len(rows))
	for _, r := range rows {
		ids = append(ids, r.VloggerID)
		counts[r.VloggerID] = r.Total
	}
	return ids, counts, nil
}
