package services

import (
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"feed/repos"
)

// 扇出策略参数：粉丝数不超过阈值的作者，发布时把视频写进粉丝时间线；
// 超过阈值的作者不推，由读者在读取时实时聚合，避免大 V 发布时的写扩散。
const (
	defaultFanoutThreshold = 1000
	timelineTTL            = 7 * 24 * time.Hour
	timelineBackfillLimit  = 20
)

// fanoutThreshold 读取推送阈值。压测时可用 FEED_FANOUT_THRESHOLD 切换纯推与纯拉：
// 设成 0 表示所有作者都走拉取，设成一个很大的值表示全部推送。
func fanoutThreshold() int {
	if v := os.Getenv("FEED_FANOUT_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return defaultFanoutThreshold
}

func timelineKey(userID uint) string {
	return "timeline:" + strconv.Itoa(int(userID))
}

// fanoutVideo 发布时把视频写进粉丝的时间线；粉丝数超过阈值的作者跳过。
// 返回错误表示这次扇出没有完成，调用方据此重试，而不是当作已完成。
func fanoutVideo(authorID, videoID uint) error {
	if !cacheEnabled() {
		return nil
	}
	count, err := repos.CountFollowers(authorID)
	if err != nil {
		return err
	}
	if count == 0 || int(count) > fanoutThreshold() {
		return nil
	}
	followerIDs, err := repos.FindFollowerIDs(authorID)
	if err != nil {
		return err
	}
	if len(followerIDs) == 0 {
		return nil
	}
	return pushToTimelines(followerIDs, []uint{videoID})
}

// backfillTimeline 关注时把对方最近的视频写进我的时间线，避免只看到关注之后的内容
func backfillTimeline(followerID, authorID uint) {
	if !cacheEnabled() {
		return
	}
	count, err := repos.CountFollowers(authorID)
	if err != nil || int(count) > fanoutThreshold() {
		return
	}
	ids, err := repos.FindRecentVideoIDsByAuthor(authorID, timelineBackfillLimit)
	if err != nil || len(ids) == 0 {
		return
	}
	pushToTimelines([]uint{followerID}, ids)
}

// clearAuthorFromTimeline 取关时把这个作者的视频从我的时间线里摘掉，避免读到已取关的人
func clearAuthorFromTimeline(followerID, authorID uint) {
	if !cacheEnabled() {
		return
	}
	ids, err := repos.FindVideoIDsByAuthor(authorID)
	if err != nil || len(ids) == 0 {
		return
	}
	members := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		members = append(members, strconv.Itoa(int(id)))
	}
	rdb.ZRem(ctx, timelineKey(followerID), members...)
}

// pushToTimelines 把一批视频写进一批用户的时间线，分值用视频 id，天然按时间有序
func pushToTimelines(userIDs, videoIDs []uint) error {
	pipe := rdb.Pipeline()
	for _, uid := range userIDs {
		key := timelineKey(uid)
		for _, vid := range videoIDs {
			pipe.ZAdd(ctx, key, redis.Z{Score: float64(vid), Member: strconv.Itoa(int(vid))})
		}
		pipe.Expire(ctx, key, timelineTTL)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// timelinePage 从时间线按游标取一页视频 id（降序）
func timelinePage(userID, cursor uint, limit int) ([]uint, error) {
	max := "+inf"
	if cursor > 0 {
		max = "(" + strconv.Itoa(int(cursor))
	}
	members, err := rdb.ZRevRangeByScore(ctx, timelineKey(userID), &redis.ZRangeBy{
		Max:    max,
		Min:    "-inf",
		Offset: 0,
		Count:  int64(limit),
	}).Result()
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		n, err := strconv.ParseUint(m, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, uint(n))
	}
	return ids, nil
}
