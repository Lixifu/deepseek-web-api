package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// SharedQueue coordinates browser admission across multiple API instances.
// Queue entries contain operational metadata only, never prompts or credentials.
type SharedQueue interface {
	Acquire(ctx context.Context) (SharedQueueLease, error)
	Waiting(ctx context.Context) (int, error)
}

type SharedQueueLease interface {
	Release()
}

type RedisSharedQueueConfig struct {
	KeyPrefix    string
	InstanceID   string
	Capacity     int
	MaxQueue     int
	WaitTimeout  time.Duration
	LeaseTTL     time.Duration
	PollInterval time.Duration
}

type RedisSharedQueue struct {
	client       *redis.Client
	logger       *zap.Logger
	instanceID   string
	capacity     int
	maxQueue     int
	waitTimeout  time.Duration
	leaseTTL     time.Duration
	pollInterval time.Duration
	orderKey     string
	expiryKey    string
	metaKey      string
	sequenceKey  string
}

type redisSharedQueueLease struct {
	queue  *RedisSharedQueue
	ticket string
	stop   chan struct{}
	once   sync.Once
}

var redisQueueEnqueueScript = redis.NewScript(`
local expired = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[2])
for _, id in ipairs(expired) do
  redis.call('ZREM', KEYS[1], id)
  redis.call('ZREM', KEYS[2], id)
  redis.call('HDEL', KEYS[3], id)
end
local count = redis.call('ZCARD', KEYS[1])
local limit = tonumber(ARGV[4])
if limit > 0 and count >= limit then
  return {-1, count}
end
local sequence = redis.call('INCR', KEYS[4])
redis.call('ZADD', KEYS[1], sequence, ARGV[1])
redis.call('ZADD', KEYS[2], tonumber(ARGV[2]) + tonumber(ARGV[3]), ARGV[1])
redis.call('HSET', KEYS[3], ARGV[1], ARGV[5])
return {0, count + 1}
`)

var redisQueueRankScript = redis.NewScript(`
local expired = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[2])
for _, id in ipairs(expired) do
  redis.call('ZREM', KEYS[1], id)
  redis.call('ZREM', KEYS[2], id)
  redis.call('HDEL', KEYS[3], id)
end
if not redis.call('ZSCORE', KEYS[1], ARGV[1]) then
  return {-2, redis.call('ZCARD', KEYS[1])}
end
redis.call('ZADD', KEYS[2], tonumber(ARGV[2]) + tonumber(ARGV[3]), ARGV[1])
return {redis.call('ZRANK', KEYS[1], ARGV[1]), redis.call('ZCARD', KEYS[1])}
`)

var redisQueueReleaseScript = redis.NewScript(`
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('HDEL', KEYS[3], ARGV[1])
return 1
`)

var redisQueueLengthScript = redis.NewScript(`
local expired = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1])
for _, id in ipairs(expired) do
  redis.call('ZREM', KEYS[1], id)
  redis.call('ZREM', KEYS[2], id)
  redis.call('HDEL', KEYS[3], id)
end
return redis.call('ZCARD', KEYS[1])
`)

func NewRedisSharedQueue(client *redis.Client, cfg RedisSharedQueueConfig, logger *zap.Logger) (*RedisSharedQueue, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: redis client is nil", ErrSharedQueueUnavailable)
	}
	if cfg.Capacity < 1 {
		return nil, errorsNewQueueConfig("capacity must be positive")
	}
	if cfg.MaxQueue < 0 {
		return nil, errorsNewQueueConfig("max queue must be non-negative")
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 60 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 100 * time.Millisecond
	}
	if cfg.InstanceID == "" {
		host, _ := os.Hostname()
		cfg.InstanceID = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	prefix := sanitizeRedisQueuePrefix(cfg.KeyPrefix)
	hashTag := "{" + prefix + "}:browser_queue"
	queue := &RedisSharedQueue{
		client:       client,
		logger:       logger,
		instanceID:   cfg.InstanceID,
		capacity:     cfg.Capacity,
		maxQueue:     cfg.MaxQueue,
		waitTimeout:  cfg.WaitTimeout,
		leaseTTL:     cfg.LeaseTTL,
		pollInterval: cfg.PollInterval,
		orderKey:     hashTag + ":order",
		expiryKey:    hashTag + ":expiry",
		metaKey:      hashTag + ":metadata",
		sequenceKey:  hashTag + ":sequence",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := queue.Waiting(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSharedQueueUnavailable, err)
	}
	return queue, nil
}

func errorsNewQueueConfig(message string) error {
	return fmt.Errorf("invalid redis shared queue configuration: %s", message)
}

func sanitizeRedisQueuePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "deepseek_web_api"
	}
	replacer := strings.NewReplacer("{", "_", "}", "_", " ", "_", "\t", "_", "\n", "_")
	return replacer.Replace(prefix)
}

func (q *RedisSharedQueue) Acquire(ctx context.Context) (SharedQueueLease, error) {
	ticket := uuid.NewString()
	payload, _ := json.Marshal(map[string]any{
		"instance_id": q.instanceID,
		"enqueued_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	maxEntries := 0
	if q.maxQueue > 0 {
		maxEntries = q.capacity + q.maxQueue
	}
	now := time.Now().UnixMilli()
	result, err := redisQueueEnqueueScript.Run(
		ctx,
		q.client,
		[]string{q.orderKey, q.expiryKey, q.metaKey, q.sequenceKey},
		ticket,
		now,
		q.leaseTTL.Milliseconds(),
		maxEntries,
		string(payload),
	).Result()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrSharedQueueUnavailable, err)
	}
	values, err := queueScriptPair(result)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSharedQueueUnavailable, err)
	}
	if values[0] == -1 {
		return nil, ErrQueueFull
	}

	keepTicket := false
	defer func() {
		if !keepTicket {
			q.releaseTicket(ticket)
		}
	}()

	ticker := time.NewTicker(q.pollInterval)
	defer ticker.Stop()
	var timeout *time.Timer
	var timeoutC <-chan time.Time
	if q.waitTimeout > 0 {
		timeout = time.NewTimer(q.waitTimeout)
		timeoutC = timeout.C
		defer timeout.Stop()
	}

	for {
		rank, err := q.rank(ctx, ticket)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if rank >= 0 && rank < int64(q.capacity) {
			keepTicket = true
			lease := &redisSharedQueueLease{
				queue:  q,
				ticket: ticket,
				stop:   make(chan struct{}),
			}
			go lease.keepAlive()
			return lease, nil
		}
		if rank == -2 {
			return nil, fmt.Errorf("%w: queue lease expired while waiting", ErrSharedQueueUnavailable)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeoutC:
			return nil, ErrQueueTimeout
		case <-ticker.C:
		}
	}
}

func (q *RedisSharedQueue) rank(ctx context.Context, ticket string) (int64, error) {
	result, err := redisQueueRankScript.Run(
		ctx,
		q.client,
		[]string{q.orderKey, q.expiryKey, q.metaKey},
		ticket,
		time.Now().UnixMilli(),
		q.leaseTTL.Milliseconds(),
	).Result()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSharedQueueUnavailable, err)
	}
	values, err := queueScriptPair(result)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSharedQueueUnavailable, err)
	}
	return values[0], nil
}

func (q *RedisSharedQueue) Waiting(ctx context.Context) (int, error) {
	result, err := redisQueueLengthScript.Run(
		ctx,
		q.client,
		[]string{q.orderKey, q.expiryKey, q.metaKey},
		time.Now().UnixMilli(),
	).Int64()
	if err != nil {
		return 0, err
	}
	waiting := result - int64(q.capacity)
	if waiting < 0 {
		waiting = 0
	}
	return int(waiting), nil
}

func queueScriptPair(result any) ([2]int64, error) {
	var values [2]int64
	items, ok := result.([]any)
	if !ok || len(items) != 2 {
		return values, fmt.Errorf("unexpected redis script result %T", result)
	}
	for i := range values {
		switch value := items[i].(type) {
		case int64:
			values[i] = value
		case string:
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return values, err
			}
			values[i] = parsed
		default:
			return values, fmt.Errorf("unexpected redis script value %T", items[i])
		}
	}
	return values, nil
}

func (q *RedisSharedQueue) releaseTicket(ticket string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := redisQueueReleaseScript.Run(
		ctx,
		q.client,
		[]string{q.orderKey, q.expiryKey, q.metaKey},
		ticket,
	).Err(); err != nil && q.logger != nil {
		q.logger.Warn("release redis shared queue lease", zap.String("ticket", ticket), zap.Error(err))
	}
}

func (lease *redisSharedQueueLease) Release() {
	lease.once.Do(func() {
		close(lease.stop)
		lease.queue.releaseTicket(lease.ticket)
	})
}

func (lease *redisSharedQueueLease) keepAlive() {
	interval := lease.queue.leaseTTL / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-lease.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			rank, err := lease.queue.rank(ctx, lease.ticket)
			cancel()
			if err != nil {
				if lease.queue.logger != nil {
					lease.queue.logger.Warn("refresh redis shared queue lease", zap.Error(err))
				}
				continue
			}
			if rank == -2 {
				if lease.queue.logger != nil {
					lease.queue.logger.Warn("redis shared queue lease expired", zap.String("ticket", lease.ticket))
				}
				return
			}
		}
	}
}
