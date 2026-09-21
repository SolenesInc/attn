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
	c.preemptSweep()
	c.gate.RLock()
	defer c.gate.RUnlock()
	return run(foregroundCleanupProtection{ctx: ctx})
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
		if c.sweepID == sweepID {
			c.sweepCancel = nil
		}
		c.mu.Unlock()
	}()

	return run(&worktreeSweepLease{coordinator: c, ctx: sweepCtx})
}

func (s *worktreeSweepLease) Context() context.Context {
	return s.ctx
}
