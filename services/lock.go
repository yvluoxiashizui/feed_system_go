package services

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

// 分布式锁参数。锁的过期时间要比回源耗时长，但也不能太长：
// 太长会让持锁者崩溃后其他实例白等，太短会让业务还没做完锁就失效。
const (
	lockTTL          = 3 * time.Second
	lockWaitTotal    = 100 * time.Millisecond
	lockPollInterval = 10 * time.Millisecond
)

// 释放锁必须比对 value 再删。直接 DEL 会误删：A 的锁超时自动释放、B 拿到锁、
// A 处理完执行 DEL，删掉的就是 B 的锁。
var releaseLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// randomLockToken 每次加锁生成唯一标识，用于释放时校验
func randomLockToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format(time.RFC3339Nano)
	}
	return hex.EncodeToString(buf)
}

// tryLock 尝试获取锁，不等待。拿到返回释放函数，没拿到返回 false，
// 由调用方决定是等缓存还是直接回源。
func tryLock(key string) (func(), bool) {
	token := randomLockToken()
	ok, err := rdb.SetNX(ctx, key, token, lockTTL).Result()
	if err != nil || !ok {
		return nil, false
	}
	return func() {
		releaseLockScript.Run(ctx, rdb, []string{key}, token)
	}, true
}
