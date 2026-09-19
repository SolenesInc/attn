package daemon

import (
	"context"
	"errors"
	"sync"
)

var errWorktreeSweepPreempted = errors.New("worktree sweep preempted")

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

func (d *Daemon) runWorktreeForeground(operation string, run func(context.Context)) {
	_ = d.worktreeMaintenance.RunForeground(context.Background(), operation, func(ctx context.Context) error {
		run(ctx)
		return nil
	})
}

func (c *worktreeMaintenanceCoordinator) RunForeground(
	ctx context.Context,
	operation string,
	run func(context.Context) error,
) error {
	c.mu.Lock()
	if c.sweepCancel != nil {
		c.sweepCancel(errWorktreeSweepPreempted)
	}
	c.mu.Unlock()

	c.gate.RLock()
	defer c.gate.RUnlock()
	return run(ctx)
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

func (s *worktreeSweepLease) TryDelete(
	finalCheck func(context.Context) error,
	commit func(context.Context) error,
) error {
	if cause := context.Cause(s.ctx); cause != nil {
		return cause
	}
	if !s.coordinator.gate.TryLock() {
		return errWorktreeSweepPreempted
	}
	defer s.coordinator.gate.Unlock()

	ctx := context.WithoutCancel(s.ctx)
	if err := finalCheck(ctx); err != nil {
		return err
	}
	return commit(ctx)
}
