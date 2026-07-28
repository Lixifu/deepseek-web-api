package core

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func TestRedisSharedQueueIntegration(t *testing.T) {
	address := os.Getenv("REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	defer client.Close()

	queue, err := NewRedisSharedQueue(client, RedisSharedQueueConfig{
		KeyPrefix:    "test_" + uuid.NewString(),
		Capacity:     1,
		MaxQueue:     1,
		WaitTimeout:  3 * time.Second,
		LeaseTTL:     5 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	first, err := queue.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	secondResult := make(chan SharedQueueLease, 1)
	secondErrors := make(chan error, 1)
	go func() {
		lease, acquireErr := queue.Acquire(context.Background())
		secondResult <- lease
		secondErrors <- acquireErr
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		waiting, waitErr := queue.Waiting(context.Background())
		if waitErr != nil {
			t.Fatal(waitErr)
		}
		if waiting == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting, _ := queue.Waiting(context.Background()); waiting != 1 {
		t.Fatalf("Waiting() = %d, want 1", waiting)
	}
	if _, err := queue.Acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third Acquire() error = %v, want ErrQueueFull", err)
	}

	first.Release()
	if err := <-secondErrors; err != nil {
		t.Fatal(err)
	}
	second := <-secondResult
	if second == nil {
		t.Fatal("second lease is nil")
	}
	second.Release()
}
