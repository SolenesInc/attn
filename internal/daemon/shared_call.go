package daemon

import (
	"context"
	"sync"
)

type sharedCall[V any] struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	value   V
	err     error
}

type sharedCalls[K comparable, V any] struct {
	mu     sync.Mutex
	root   context.Context
	active map[K]*sharedCall[V]
}

func newSharedCalls[K comparable, V any](root context.Context) *sharedCalls[K, V] {
	if root == nil {
		root = context.Background()
	}
	return &sharedCalls[K, V]{root: root, active: make(map[K]*sharedCall[V])}
}

func (s *sharedCalls[K, V]) Do(ctx context.Context, key K, run func(context.Context) (V, error)) (V, error) {
	s.mu.Lock()
	call, ok := s.active[key]
	if ok {
		call.waiters++
		s.mu.Unlock()
	} else {
		callCtx, cancel := context.WithCancel(s.root)
		call = &sharedCall[V]{ctx: callCtx, cancel: cancel, done: make(chan struct{}), waiters: 1}
		s.active[key] = call
		s.mu.Unlock()
		go s.run(key, call, run)
	}

	select {
	case <-call.done:
		return call.value, call.err
	case <-ctx.Done():
		s.leave(key, call)
		var zero V
		return zero, context.Cause(ctx)
	}
}

func (s *sharedCalls[K, V]) run(key K, call *sharedCall[V], run func(context.Context) (V, error)) {
	call.value, call.err = run(call.ctx)
	s.mu.Lock()
	if s.active[key] == call {
		delete(s.active, key)
	}
	close(call.done)
	s.mu.Unlock()
	call.cancel()
}

func (s *sharedCalls[K, V]) leave(key K, call *sharedCall[V]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[key] != call {
		return
	}
	call.waiters--
	if call.waiters == 0 {
		delete(s.active, key)
		call.cancel()
	}
}
