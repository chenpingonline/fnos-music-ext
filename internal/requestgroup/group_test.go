package requestgroup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitWaiters[T any](t *testing.T, g *Group[T], key string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		n := 0
		if c := g.calls[key]; c != nil {
			n = c.waiters
		}
		g.mu.Unlock()
		if n == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("waiters did not join")
}
func TestSharedWorkSurvivesFirstCancellation(t *testing.T) {
	var g Group[int]
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (int, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	first, cancel := context.WithCancel(context.Background())
	a, b := make(chan error, 1), make(chan int, 1)
	go func() { _, e := g.Do(first, "same", time.Second, load); a <- e }()
	<-entered
	go func() {
		v, e := g.Do(context.Background(), "same", time.Second, load)
		if e != nil {
			b <- -1
		} else {
			b <- v
		}
	}()
	waitWaiters(t, &g, "same", 2)
	cancel()
	if !errors.Is(<-a, context.Canceled) {
		t.Fatal("first did not cancel")
	}
	close(release)
	if <-b != 42 || calls.Load() != 1 {
		t.Fatal("shared work cancelled or duplicated", calls.Load())
	}
}
func TestAllWaitersCancelWorkerAndNewRequestCanRetry(t *testing.T) {
	var g Group[int]
	workerDone := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := g.Do(ctx, "x", time.Second, func(ctx context.Context) (int, error) { <-ctx.Done(); close(workerDone); return 0, ctx.Err() })
		done <- err
	}()
	waitWaiters(t, &g, "x", 1)
	cancel()
	<-done
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("worker leaked")
	}
	got, err := g.Do(context.Background(), "x", time.Second, func(context.Context) (int, error) { return 7, nil })
	if err != nil || got != 7 {
		t.Fatal(got, err)
	}
}
