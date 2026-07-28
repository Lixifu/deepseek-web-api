package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSharedQueueLease struct {
	mu       sync.Mutex
	released int
}

func (lease *fakeSharedQueueLease) Release() {
	lease.mu.Lock()
	lease.released++
	lease.mu.Unlock()
}

type fakeSharedQueue struct {
	lease   *fakeSharedQueueLease
	waiting int
	err     error
}

func (queue *fakeSharedQueue) Acquire(context.Context) (SharedQueueLease, error) {
	if queue.err != nil {
		return nil, queue.err
	}
	return queue.lease, nil
}

func (queue *fakeSharedQueue) Waiting(context.Context) (int, error) {
	return queue.waiting, queue.err
}

func TestBrowserPoolAcquireWaitsUntilSessionReleased(t *testing.T) {
	session := &BrowserSession{AccountID: 1, healthy: true, busy: true}
	pool := &BrowserPool{
		sessions:     []*BrowserSession{session},
		maxQueue:     2,
		queueTimeout: time.Second,
	}
	session.onRelease = pool.notifyWaiters

	result := make(chan *BrowserSession, 1)
	errs := make(chan error, 1)
	go func() {
		acquired, err := pool.Acquire(context.Background(), map[uint]bool{})
		result <- acquired
		errs <- err
	}()

	deadline := time.Now().Add(time.Second)
	for pool.QueueLength() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if pool.QueueLength() != 1 {
		t.Fatal("request was not queued")
	}
	session.Release()
	if err := <-errs; err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if acquired := <-result; acquired != session {
		t.Fatalf("Acquire() session = %p, want %p", acquired, session)
	}
	session.Release()
}

func TestBrowserPoolAcquireReturnsNoSessionWhenUnhealthy(t *testing.T) {
	session := &BrowserSession{AccountID: 1, healthy: false}
	pool := &BrowserPool{sessions: []*BrowserSession{session}}

	_, err := pool.Acquire(context.Background(), map[uint]bool{})
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("Acquire() error = %v, want ErrNoSession", err)
	}
}

func TestBrowserPoolQueuedCallerCanTriggerRestartWhenSessionTurnsUnhealthy(t *testing.T) {
	session := &BrowserSession{AccountID: 1, healthy: true, busy: true}
	pool := &BrowserPool{
		sessions:     []*BrowserSession{session},
		maxQueue:     1,
		queueTimeout: time.Second,
	}
	session.onRelease = pool.notifyWaiters

	errs := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(context.Background(), map[uint]bool{})
		errs <- err
	}()

	deadline := time.Now().Add(time.Second)
	for pool.QueueLength() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	session.MarkUnhealthy()
	session.Release()

	if err := <-errs; !errors.Is(err, ErrNoSession) {
		t.Fatalf("Acquire() error = %v, want ErrNoSession", err)
	}
}

func TestBrowserPoolQueueFull(t *testing.T) {
	session := &BrowserSession{AccountID: 1, healthy: true, busy: true}
	pool := &BrowserPool{
		sessions:     []*BrowserSession{session},
		maxQueue:     1,
		queueTimeout: time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = pool.Acquire(ctx, map[uint]bool{})
	}()
	deadline := time.Now().Add(time.Second)
	for pool.QueueLength() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	_, err := pool.Acquire(context.Background(), map[uint]bool{})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Acquire() error = %v, want ErrQueueFull", err)
	}
}

func TestBrowserPoolQueueTimeout(t *testing.T) {
	session := &BrowserSession{AccountID: 1, healthy: true, busy: true}
	pool := &BrowserPool{
		sessions:     []*BrowserSession{session},
		maxQueue:     1,
		queueTimeout: 10 * time.Millisecond,
	}
	_, err := pool.Acquire(context.Background(), map[uint]bool{})
	if !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("Acquire() error = %v, want ErrQueueTimeout", err)
	}
	if pool.QueueLength() != 0 {
		t.Fatalf("QueueLength() = %d, want 0", pool.QueueLength())
	}
}

func TestBrowserPoolQueueIsFIFO(t *testing.T) {
	session := &BrowserSession{AccountID: 1, healthy: true, busy: true}
	pool := &BrowserPool{
		sessions:     []*BrowserSession{session},
		maxQueue:     3,
		queueTimeout: time.Second,
	}
	session.onRelease = pool.notifyWaiters

	order := make(chan int, 2)
	for id := 1; id <= 2; id++ {
		id := id
		go func() {
			acquired, err := pool.Acquire(context.Background(), map[uint]bool{})
			if err != nil {
				order <- -id
				return
			}
			order <- id
			acquired.Release()
		}()
		deadline := time.Now().Add(time.Second)
		for pool.QueueLength() != id && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	session.Release()
	first, second := <-order, <-order
	if first != 1 || second != 2 {
		t.Fatalf("acquisition order = [%d %d], want [1 2]", first, second)
	}
}

func TestBrowserPoolSkipsRedundantRestart(t *testing.T) {
	pool := &BrowserPool{generation: 2}
	restarted, err := pool.RestartIfGeneration(1)
	if err != nil {
		t.Fatalf("RestartIfGeneration() error = %v", err)
	}
	if restarted {
		t.Fatal("RestartIfGeneration() restarted a newer browser generation")
	}
}

func TestBrowserPoolReleasesSharedQueueLeaseWithSession(t *testing.T) {
	lease := &fakeSharedQueueLease{}
	session := &BrowserSession{AccountID: 1, healthy: true}
	pool := &BrowserPool{
		sessions:    []*BrowserSession{session},
		sharedQueue: &fakeSharedQueue{lease: lease},
	}

	acquired, err := pool.Acquire(context.Background(), map[uint]bool{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	acquired.Release()

	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released != 1 {
		t.Fatalf("lease released %d times, want 1", lease.released)
	}
}

func TestBrowserPoolUsesSharedQueueLength(t *testing.T) {
	pool := &BrowserPool{sharedQueue: &fakeSharedQueue{waiting: 7}}
	if got := pool.EffectiveQueueLength(context.Background()); got != 7 {
		t.Fatalf("EffectiveQueueLength() = %d, want 7", got)
	}
}
