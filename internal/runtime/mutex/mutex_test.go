package mutex

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Many goroutines take the same lock, like callback handlers do for one
// faucet or inline send. Never two inside at once, no waiter left behind.
func TestLockIsExclusive(t *testing.T) {
	var inside, overlaps int32
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				Lock("obj")
				if atomic.AddInt32(&inside, 1) > 1 {
					atomic.AddInt32(&overlaps, 1)
				}
				atomic.AddInt32(&inside, -1)
				Unlock("obj")
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("goroutines stuck in Lock")
	}
	if overlaps != 0 {
		t.Fatalf("%d overlaps", overlaps)
	}
	if !IsEmpty() {
		t.Fatalf("locks left behind: %v", keys())
	}
}

func TestUnlockNotHeldIsIgnored(t *testing.T) {
	if Unlock("never-locked") {
		t.Fatal("unlocked a lock that was never taken")
	}
	Lock("x")
	Unlock("x")
	if Unlock("x") {
		t.Fatal("double unlock released something")
	}
}

func TestLockWithContextIsReentrantPerObject(t *testing.T) {
	ctx := context.WithValue(context.Background(), "uid", "req1")
	LockWithContext(ctx, "a")
	LockWithContext(ctx, "a") // nested handler, same request: must not deadlock
	LockWithContext(ctx, "b") // another object in the same request is locked too
	locked := make(chan struct{})
	go func() {
		other := context.WithValue(context.Background(), "uid", "req2")
		LockWithContext(other, "b")
		close(locked)
		UnlockWithContext(other, "b")
	}()
	select {
	case <-locked:
		t.Fatal("another request got object b while req1 holds it")
	case <-time.After(100 * time.Millisecond):
	}
	UnlockWithContext(ctx, "b")
	<-locked
	UnlockWithContext(ctx, "a")
	UnlockWithContext(ctx, "a")
	time.Sleep(10 * time.Millisecond)
	if !IsEmpty() {
		t.Fatalf("locks left behind: %v", keys())
	}
}
