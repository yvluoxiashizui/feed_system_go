package services

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"

	"feed/models"
	"feed/repos"
)

var ctx = context.Background()

// cacheEnabled Feed 缓存开关，默认开启。
// 压测时设置 FEED_CACHE_DISABLED=1 可关闭缓存读取与写入，
// 用于在同一份代码上做「有缓存 / 无缓存」的读写路径对照。
func cacheEnabled() bool {
	return os.Getenv("FEED_CACHE_DISABLED") != "1"
}

// feedPage Feed 流分页数据（缓存用）
type feedPage struct {
	Videos     []models.Video `json:"videos"`
	NextCursor int            `json:"next_cursor"`
}

// 幂等键长度上限，够放 UUID 和常见客户端生成的键
const maxIdemKeyLength = 64

// 视频标题与播放地址的上限
const (
	maxVideoTitleLength = 100
	maxPlayURLLength    = 500
)

// PublishVideo 发布视频：标题和播放地址不能为空，并按字符数限制长度
func PublishVideo(userID uint, title, playURL, idemKey string) (*models.Video, error) {
	title = strings.TrimSpace(title)
	playURL = strings.TrimSpace(playURL)
	idemKey = strings.TrimSpace(idemKey)

	if title == "" {
		return nil, errors.New("标题不能为空")
	}
	if utf8.RuneCountInString(title) > maxVideoTitleLength {
		return nil, errors.New("标题最多 100 字")
	}
	if playURL == "" {
		return nil, errors.New("播放地址不能为空")
	}
	if len(playURL) > maxPlayURLLength {
		return nil, errors.New("播放地址过长")
	}
	if len(idemKey) > maxIdemKeyLength {
		return nil, errors.New("幂等键过长")
	}

	author, err := repos.FindUserByID(userID)
	if err != nil {
		return nil, errors.New("用户不存在")
	}
	v := &models.Video{AuthorID: author.ID, Username: author.Username, Title: title, PlayURL: playURL}
	if idemKey != "" {
		v.IdemKey = &idemKey
	}
	if err := repos.CreateVideo(v); err != nil {
		// 幂等键撞唯一索引说明这次是重复提交：返回第一次创建的那条，后续副作用不再执行
		if idemKey != "" && repos.IsDuplicateKey(err) {
			existing, findErr := repos.FindVideoByAuthorAndIdemKey(author.ID, idemKey)
			if findErr != nil {
				return nil, findErr
			}
			return existing, nil
		}
		return nil, err
	}

	// 窗口只缓存一页 id 列表，发布后直接删掉这个 key，新视频立即可见
	rdb.Del(ctx, feedWindowKey)

	// 写扩散移出主请求：投递给消费者异步执行；MQ 不可用时退回同步扇出，
	// 保证发布接口在任何情况下都能完成，代价只是接口耗时变长
	if err := publishFanout(author.ID, v.ID); err != nil {
		if err := fanoutVideo(author.ID, v.ID); err != nil {
			log.Println("同步扇出失败，视频已发布但未写入粉丝时间线:", err)
		}
	}

	return v, nil
}

// Feed 窗口缓存参数。窗口里存最近 N 条视频的 id（降序），
// 一个 key 覆盖前若干页，不同游标复用同一份数据。
const (
	feedWindowSize    = 500
	feedWindowKey     = "feed:recent:ids"
	feedWindowLockKey = "feed:lock:window"
	feedCacheTTL      = 30 * time.Second
	emptyFeedCacheTTL = 10 * time.Second
	videoCachePrefix  = "video:"
	videoObjectTTL    = 30 * time.Second
)

// cacheTTLWithJitter 给基础过期时间加随机抖动，避免同一批 key 同时失效
func cacheTTLWithJitter(base time.Duration) time.Duration {
	return base + time.Duration(rand.Int63n(int64(base/5)))
}

// feedSingle 同一 key 并发未命中时只放一个请求回源
var feedSingle singleflight.Group

// ListFeed 游标分页 Feed。
// 优先走热点窗口缓存：窗口里能切出这一页就直接返回；切不出来（游标落在窗口之外，
// 或者窗口边界可能截断了真实结果）再回退到数据库，用 id 游标查。
func ListFeed(userID, cursor uint, limit int) (feedPage, error) {
	var (
		page feedPage
		err  error
	)
	if cacheEnabled() {
		if ids, e := recentFeedIDs(); e == nil {
			if p, ok := feedPageFromWindow(ids, cursor, limit); ok {
				page = p
			}
		}
	}
	if page.Videos == nil && err == nil {
		page, err = loadFeedPage(cursor, limit)
	}
	if err != nil {
		return feedPage{}, err
	}
	markLiked(userID, page.Videos)
	return page, nil
}

// markLiked 给这一页视频补上「当前用户是否点过赞」。
// 游客（userID 为 0）跳过；登录用户用一次 IN 查询覆盖整页，不逐条查。
func markLiked(userID uint, videos []models.Video) {
	if userID == 0 || len(videos) == 0 {
		return
	}
	ids := make([]uint, len(videos))
	for i := range videos {
		ids[i] = videos[i].ID
	}
	liked, err := repos.LikedAmong(userID, ids)
	if err != nil {
		return
	}
	for i := range videos {
		videos[i].IsLiked = liked[videos[i].ID]
	}
}

// windowSlice 在窗口里切出一页 id。切不出来时返回 false：
// 游标比窗口里最老的 id 还小，或者没凑满一页又已经到窗口末尾（可能被窗口截断）。
func windowSlice(ids []uint, cursor uint, limit int) ([]uint, bool) {
	start := 0
	if cursor > 0 {
		start = -1
		for i, id := range ids {
			if id < cursor {
				start = i
				break
			}
		}
		if start < 0 {
			return nil, false
		}
	}

	end := start + limit
	if end > len(ids) {
		end = len(ids)
	}
	pageIDs := ids[start:end]

	// 没凑满又已经到窗口末尾，可能是窗口把真实结果截断了，回源保证正确
	if len(pageIDs) < limit && end == len(ids) {
		return nil, false
	}
	return pageIDs, true
}

// feedPageFromWindow 从窗口里切出一页；切不出来时返回 false，交给调用方回源
func feedPageFromWindow(ids []uint, cursor uint, limit int) (feedPage, bool) {
	pageIDs, ok := windowSlice(ids, cursor, limit)
	if !ok {
		return feedPage{}, false
	}

	videos, err := loadVideosByIDs(pageIDs)
	if err != nil {
		return feedPage{}, false
	}
	byID := make(map[uint]models.Video, len(videos))
	for _, v := range videos {
		byID[v.ID] = v
	}
	ordered := make([]models.Video, 0, len(pageIDs))
	for _, id := range pageIDs {
		if v, ok := byID[id]; ok {
			ordered = append(ordered, v)
		}
	}
	return feedPage{Videos: ordered, NextCursor: int(pageIDs[len(pageIDs)-1])}, true
}

// recentFeedIDs 取最近 feedWindowSize 条视频 id（降序）。
// 回源做两层收敛：单飞挡同一实例内的并发，分布式锁挡多实例之间的并发。
func recentFeedIDs() ([]uint, error) {
	if ids, ok := readFeedWindow(); ok {
		return ids, nil
	}

	v, err, _ := feedSingle.Do(feedWindowKey, func() (interface{}, error) {
		// 等锁期间可能已被前一个请求回填，再查一次缓存
		if ids, ok := readFeedWindow(); ok {
			return ids, nil
		}
		return rebuildFeedWindow()
	})
	if err != nil {
		return nil, err
	}
	return v.([]uint), nil
}

// rebuildFeedWindow 回源重建窗口。抢到锁的实例负责回填缓存，
// 没抢到的短暂等缓存出现，等不到就直接查库且不写缓存——
// 避免多个实例同时回源后又同时写，把击穿从「一起查库」变成「一起写缓存」。
func rebuildFeedWindow() ([]uint, error) {
	release, ok := tryLock(feedWindowLockKey)
	if ok {
		defer release()

		// 抢到锁之后再确认一次：等锁期间别的实例可能刚写完，这时不必重复查库
		if ids, ok := readFeedWindow(); ok {
			return ids, nil
		}
		ids, err := repos.ListRecentVideoIDs(feedWindowSize)
		if err != nil {
			return nil, err
		}
		writeFeedWindow(ids)
		return ids, nil
	}

	deadline := time.Now().Add(lockWaitTotal)
	for time.Now().Before(deadline) {
		time.Sleep(lockPollInterval)
		if ids, ok := readFeedWindow(); ok {
			return ids, nil
		}
	}

	// 持锁者迟迟没写回，退化成直查：不写缓存，避免多实例重复写
	return repos.ListRecentVideoIDs(feedWindowSize)
}

// loadFeedPage 直接查库，并算出下一页游标（窗口覆盖不到时回退走这里）
func loadFeedPage(cursor uint, limit int) (feedPage, error) {
	videos, err := repos.ListVideosByCursor(cursor, limit)
	if err != nil {
		return feedPage{}, err
	}
	page := feedPage{Videos: videos}
	if len(videos) > 0 {
		page.NextCursor = int(videos[len(videos)-1].ID)
	}
	return page, nil
}

// readFeedWindow 读窗口缓存，反序列化失败按未命中处理
func readFeedWindow() ([]uint, bool) {
	val, err := rdb.Get(ctx, feedWindowKey).Result()
	if err != nil {
		return nil, false
	}
	var ids []uint
	if err := json.Unmarshal([]byte(val), &ids); err != nil {
		return nil, false
	}
	return ids, true
}

// writeFeedWindow 写窗口缓存，过期时间加随机抖动，避免同一批写入的 key 同时失效
func writeFeedWindow(ids []uint) {
	data, err := json.Marshal(ids)
	if err != nil {
		return
	}
	base := feedCacheTTL
	if len(ids) == 0 {
		base = emptyFeedCacheTTL
	}
	rdb.Set(ctx, feedWindowKey, data, cacheTTLWithJitter(base))
}

// videoCacheKey 单个视频对象的缓存 key
func videoCacheKey(id uint) string {
	return videoCachePrefix + strconv.Itoa(int(id))
}

// loadVideosByIDs 按 id 列表取视频：先走对象缓存，未命中的批量查库并回填。
// 返回顺序和传入 ids 一致，查不到的直接跳过。
func loadVideosByIDs(ids []uint) ([]models.Video, error) {
	if len(ids) == 0 {
		return []models.Video{}, nil
	}
	if !cacheEnabled() {
		return repos.FindVideosByIDs(ids)
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = videoCacheKey(id)
	}
	vals, err := rdb.MGet(ctx, keys...).Result()
	if err != nil {
		// 缓存不可用时直接查库，不影响正确性
		return repos.FindVideosByIDs(ids)
	}

	found := make(map[uint]models.Video, len(ids))
	missIDs := make([]uint, 0, len(ids))
	for i, val := range vals {
		s, ok := val.(string)
		if !ok {
			missIDs = append(missIDs, ids[i])
			continue
		}
		var video models.Video
		if err := json.Unmarshal([]byte(s), &video); err != nil {
			missIDs = append(missIDs, ids[i])
			continue
		}
		found[video.ID] = video
	}

	if len(missIDs) > 0 {
		missVideos, err := repos.FindVideosByIDs(missIDs)
		if err != nil {
			return nil, err
		}
		ttl := cacheTTLWithJitter(videoObjectTTL)
		for _, v := range missVideos {
			found[v.ID] = v
			if data, err := json.Marshal(v); err == nil {
				rdb.Set(ctx, videoCacheKey(v.ID), data, ttl)
			}
		}
	}

	result := make([]models.Video, 0, len(ids))
	for _, id := range ids {
		if v, ok := found[id]; ok {
			result = append(result, v)
		}
	}
	return result, nil
}

// GetVideoDetail 视频详情，走视频对象缓存
func GetVideoDetail(userID, id uint) (*models.Video, error) {
	videos, err := loadVideosByIDs([]uint{id})
	if err != nil {
		return nil, err
	}
	if len(videos) == 0 {
		return nil, errors.New("视频不存在")
	}
	markLiked(userID, videos)
	return &videos[0], nil
}

// LikeVideo 点赞：先确认视频存在，再在一个事务里插记录并累加计数，最后累加热度
func LikeVideo(userID, videoID uint) error {
	if _, err := repos.FindVideoByID(videoID); err != nil {
		return errors.New("视频不存在")
	}

	duplicated, err := repos.LikeAndIncrement(userID, videoID)
	if err != nil {
		return err
	}
	if duplicated {
		return errors.New("不能重复点赞")
	}
	// 热度按分钟分桶，桶自带过期，滑出一小时窗口后自然消失
	hotIncr(videoID, 1)
	// 点赞数变了，让这个视频的对象缓存失效
	rdb.Del(ctx, videoCacheKey(videoID))
	return nil
}

// UnlikeVideo 取消点赞：在一个事务里删记录并回退计数，再回退热度
func UnlikeVideo(userID, videoID uint) error {
	removed, err := repos.UnlikeAndDecrement(userID, videoID)
	if err != nil {
		return err
	}
	if !removed {
		return errors.New("还没有点赞")
	}

	// 取消点赞在同一个分钟桶里减分，聚合时只取正分，所以不需要逐条清理成员
	hotIncr(videoID, -1)
	// 点赞数变了，让这个视频的对象缓存失效
	rdb.Del(ctx, videoCacheKey(videoID))
	return nil
}

// IsLiked 判断是否已点赞
func IsLiked(userID, videoID uint) (bool, error) {
	return repos.IsLiked(userID, videoID)
}

// LikedVideos 我赞过的视频，按点赞时间倒序，游标是点赞记录的 id
func LikedVideos(userID, cursor uint, limit int) (feedPage, error) {
	ids, next, err := repos.ListLikedVideoIDs(userID, cursor, limit)
	if err != nil {
		return feedPage{}, err
	}
	if len(ids) == 0 {
		return feedPage{Videos: []models.Video{}}, nil
	}

	videos, err := repos.FindVideosByIDs(ids)
	if err != nil {
		return feedPage{}, err
	}
	byID := make(map[uint]models.Video, len(videos))
	for _, v := range videos {
		byID[v.ID] = v
	}
	// 按点赞顺序重排，视频表的返回顺序不可依赖
	ordered := make([]models.Video, 0, len(ids))
	for _, id := range ids {
		if v, ok := byID[id]; ok {
			ordered = append(ordered, v)
		}
	}
	return feedPage{Videos: ordered, NextCursor: next}, nil
}
