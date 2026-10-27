package daemon

import (
	"context"
	"errors"
	"sync"
)

var errAutomaticWorktreeCleanupPreempted = errors.New("automatic worktree cleanup preempted")

type worktreeMaintenanceCoordinator struct {
	gate sync.RWMutex

	mu          sync.Mutex
	sweepCancel context.CancelCauseFunc
	sweepID     uint64
	protections int
	whenIdle    []func()
}

type worktreeSweepLease struct {
	coordinator *worktreeMaintenanceCoordinator
	ctx         context.Context
}

type foregroundCleanupProtection struct {
	ctx context.Context
}

type automaticWorktreeCleanupProtection struct {
	ctx context.Context
}

type worktreeCleanupProtection interface {
	Context() context.Context
}

func (p foregroundCleanupProtection) Context() context.Context {
	return p.ctx
}

func (p automaticWorktreeCleanupProtection) Context() context.Context {
	return p.ctx
}

func (c *worktreeMaintenanceCoordinator) ProtectFromAutomaticCleanup(
	ctx context.Context,
	run func(foregroundCleanupProtection) error,
) error {
	c.mu.Lock()
	if c.sweepCancel != nil {
		c.sweepCancel(errAutomaticWorktreeCleanupPreempted)
	}
	c.protections++
	c.mu.Unlock()
	defer c.releaseProtection()
	c.gate.RLock()
	defer c.gate.RUnlock()
	return run(foregroundCleanupProtection{ctx: ctx})
}

func (c *worktreeMaintenanceCoordinator) releaseProtection() {
	c.mu.Lock()
	c.protections--
	idle := c.takeWhenIdleLocked()
	c.mu.Unlock()
	for _, fn := range idle {
		fn()
	}
}

func (c *worktreeMaintenanceCoordinator) takeWhenIdleLocked() []func() {
	if c.protections > 0 || c.sweepCancel != nil {
		return nil
	}
	idle := c.whenIdle
	c.whenIdle = nil
	return idle
}

func (c *worktreeMaintenanceCoordinator) preemptSweep() {
	c.mu.Lock()
	if c.sweepCancel != nil {
		c.sweepCancel(errAutomaticWorktreeCleanupPreempted)
	}
	c.mu.Unlock()
}

func (c *worktreeMaintenanceCoordinator) TryAutomaticRemoval(
	ctx context.Context,
	run func(automaticWorktreeCleanupProtection) error,
) error {
	c.preemptSweep()
	return c.tryRemoval(ctx, run)
}

func (c *worktreeMaintenanceCoordinator) TryBackgroundRemoval(
	ctx context.Context,
	run func(automaticWorktreeCleanupProtection) error,
) error {
	// Hold mu through removal so a sweep cannot start after the active-lease check.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sweepCancel != nil {
		return errAutomaticWorktreeCleanupPreempted
	}
	return c.tryRemoval(ctx, run)
}

// RunWhenIdle reports whether a sweep or a foreground protection is active; if so, fn runs once none is.
func (c *worktreeMaintenanceCoordinator) RunWhenIdle(fn func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.protections == 0 && c.sweepCancel == nil {
		return false
	}
	c.whenIdle = append(c.whenIdle, fn)
	return true
}

func (c *worktreeMaintenanceCoordinator) tryRemoval(
	ctx context.Context,
	run func(automaticWorktreeCleanupProtection) error,
) error {
	if !c.gate.TryLock() {
		return errAutomaticWorktreeCleanupPreempted
	}
	defer c.gate.Unlock()
	return run(automaticWorktreeCleanupProtection{ctx: ctx})
}

func (s *worktreeSweepLease) TryAutomaticRemoval(
	run func(automaticWorktreeCleanupProtection) error,
) error {
	if cause := context.Cause(s.ctx); cause != nil {
		return cause
	}
	if !s.coordinator.gate.TryLock() {
		return errAutomaticWorktreeCleanupPreempted
	}
	defer s.coordinator.gate.Unlock()
	if cause := context.Cause(s.ctx); cause != nil {
		return cause
	}
	return run(automaticWorktreeCleanupProtection{ctx: context.WithoutCancel(s.ctx)})
}

func (c *worktreeMaintenanceCoordinator) RunSweep(
	ctx context.Context,
	run func(*worktreeSweepLease) error,
) error {
	sweepCtx, cancel := context.WithCancelCause(ctx)
	c.mu.Lock()
	c.sweepID++
	sweepID := c.sweepID
	c.sweepCancel = cancel
	c.mu.Unlock()

	defer func() {
		cancel(nil)
		c.mu.Lock()
		var idle []func()
		if c.sweepID == sweepID {
			c.sweepCancel = nil
			idle = c.takeWhenIdleLocked()
		}
		c.mu.Unlock()
		for _, fn := range idle {
			fn()
		}
	}()

	return run(&worktreeSweepLease{coordinator: c, ctx: sweepCtx})
}

func (s *worktreeSweepLease) Context() context.Context {
	return s.ctx
}
