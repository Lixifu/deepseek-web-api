package core

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter 基于 Redis 的固定窗口限流器
type RateLimiter struct {
	client *redis.Client
	limit  int           // 窗口内允许次数
	window time.Duration // 窗口大小
}

func NewRateLimiter(client *redis.Client, limitPerMinute int) *RateLimiter {
	return &RateLimiter{
		client: client,
		limit:  limitPerMinute,
		window: time.Minute,
	}
}

// Allow 检查 key（通常是 api_key_id）是否被允许调用。
// 返回 (allowed, remaining, error)
func (rl *RateLimiter) Allow(ctx context.Context, key string) (bool, int, error) {
	if rl == nil || rl.client == nil || rl.limit <= 0 {
		return true, 0, nil
	}
	now := time.Now().UnixNano()
	windowStart := now - int64(rl.window)
	member := fmt.Sprintf("%d", now)

	// Lua 脚本：移除窗口外成员 + 计数 + 添加当前成员
	script := redis.NewScript(`
		local key = KEYS[1]
		local window_start = tonumber(ARGV[1])
		local member = ARGV[2]
		local limit = tonumber(ARGV[3])
		redis.call('ZREMRANGEBYSCORE', key, 0, window_start)
		local cnt = redis.call('ZCARD', key)
		if cnt >= limit then
			return {0, cnt}
		end
		redis.call('ZADD', key, member, member)
		redis.call('PEXPIRE', key, tonumber(ARGV[4]))
		return {1, cnt + 1}
	`)
	res, err := script.Run(ctx, rl.client, []string{key},
		windowStart, member, rl.limit, int64(rl.window/time.Millisecond)).Result()
	if err != nil {
		return true, 0, err // 限流器故障时放行，避免阻断业务
	}
	vals, ok := res.([]any)
	if !ok || len(vals) < 2 {
		return true, 0, fmt.Errorf("unexpected rate limit result")
	}
	allowed := vals[0].(int64) == 1
	remaining := int(vals[1].(int64))
	return allowed, remaining, nil
}
