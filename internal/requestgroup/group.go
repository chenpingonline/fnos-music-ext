// Package requestgroup shares concurrent work, without coupling the worker to
// the first waiter's cancellation. The worker stops when all waiters leave.
package requestgroup

import (
	"context"
	"errors"
	"sync"
	"time"
)

type call[T any] struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	value   T
	err     error
}
type Group[T any] struct {
	mu    sync.Mutex
	calls map[string]*call[T]
}

func (g *Group[T]) Do(ctx context.Context, key string, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]*call[T]{}
	}
	c := g.calls[key]
	if c == nil || c.waiters == 0 {
		if len(g.calls) >= 512 {
			g.mu.Unlock()
			return zero, errors.New("too many concurrent source requests")
		}
		shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		c = &call[T]{done: make(chan struct{}), cancel: cancel}
		g.calls[key] = c
		go func(c *call[T]) {
			value, err := fn(shared)
			cancel()
			g.mu.Lock()
			c.value, c.err = value, err
			if g.calls[key] == c {
				delete(g.calls, key)
			}
			close(c.done)
			g.mu.Unlock()
		}(c)
	}
	c.waiters++
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		g.mu.Lock()
		c.waiters--
		if c.waiters == 0 {
			c.cancel()
			if g.calls[key] == c {
				delete(g.calls, key)
			}
		}
		g.mu.Unlock()
		return zero, ctx.Err()
	case <-c.done:
		return c.value, c.err
	}
}
