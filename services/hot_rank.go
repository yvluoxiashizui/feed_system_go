package services

import (
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"feed/models"
)

// 热榜滑动窗口：点赞按分钟落到不同的桶里，读取时聚合最近一小时，聚合结果再缓存。
// 单个 ZSET 只能累加，看不出「最近一小时」；分桶之后热度天然随时间滑出窗口。
const (
	hotBucketPrefix  = "hot:bucket:"
	hotSnapshotKey   = "hot:snapshot:1h"
	hotWindowMinutes = 60
	hotBucketTTL     = 2 * time.Hour
	hotSnapshotTTL   = 30 * time.Second
	hotMaxLimit      = 50
	hotMaxOffset     = 100
	hotDefaultLimit  = 10
)

var hotSnapshotGroup singleflight.Group

// hotBucketKey 分钟桶的键，带年月日时分，便于直接看出是哪一分钟的数据
func hotBucketKey(t time.Time) string {
	return hotBucketPrefix + t.Format("200601021504")
}

// hotIncr 在当前分钟的桶上加减热度。桶自带过期时间，滑出窗口后自动消失。
func hotIncr(videoID uint, delta int) {
	if delta == 0 {
		return
	}
	key := hotBucketKey(time.Now())
	rdb.ZIncrBy(ctx, key, float64(delta), strconv.FormatUint(uint64(videoID), 10))
	rdb.Expire(ctx, key, hotBucketTTL)
}

// recentHotBuckets 从当前分钟往前推，取出窗口内的所有桶键
func recentHotBuckets(now time.Time) []string {
	keys := make([]string, 0, hotWindowMinutes)
	for i := 0; i < hotWindowMinutes; i++ {
		keys = append(keys, hotBucketKey(now.Add(-time.Duration(i)*time.Minute)))
	}
	return keys
}

// rebuildHotSnapshot 把最近一小时的桶合并成一张快照，并设短过期
func rebuildHotSnapshot() error {
	if err := rdb.ZUnionStore(ctx, hotSnapshotKey, &redis.ZStore{
		Keys:      recentHotBuckets(time.Now()),
		Aggregate: "SUM",
	}).Err(); err != nil {
		return err
	}
	rdb.Expire(ctx, hotSnapshotKey, hotSnapshotTTL)
	return nil
}

// HotRank 热榜分页。榜单深度有上限（offset 与 limit 都被截断到上限），
// 所以这里用偏移分页；Feed 那种能翻到几千条的列表才必须上复合游标。
func HotRank(offset, limit int) ([]models.Video, error) {
	if offset < 0 {
		offset = 0
	}
	if offset > hotMaxOffset {
		offset = hotMaxOffset
	}
	if limit < 1 {
		limit = hotDefaultLimit
	}
	if limit > hotMaxLimit {
		limit = hotMaxLimit
	}

	exists, err := rdb.Exists(ctx, hotSnapshotKey).Result()
	if err != nil {
		return nil, err
	}
	if exists == 0 {
		// 并发重建同一张快照没有意义，用单飞收敛成一次聚合
		if _, err, _ := hotSnapshotGroup.Do("hot-snapshot", func() (interface{}, error) {
			return nil, rebuildHotSnapshot()
		}); err != nil {
			return nil, err
		}
	}

	// 只取正分：取消点赞会把桶里的分值减下来，聚合后可能出现 0 或负数
	ids, err := rdb.ZRevRangeByScore(ctx, hotSnapshotKey, &redis.ZRangeBy{
		Max:    "+inf",
		Min:    "(0",
		Offset: int64(offset),
		Count:  int64(limit),
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Video{}, nil
	}

	videoIDs := make([]uint, 0, len(ids))
	for _, id := range ids {
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			continue
		}
		videoIDs = append(videoIDs, uint(n))
	}
	// loadVideosByIDs 保持传入顺序，所以拿到的就是热度次序
	return loadVideosByIDs(videoIDs)
}
