package jobs

import "sync"

type CommitGuard struct {
	mu         sync.Mutex
	cancelled  bool
	committing bool
}

func (g *CommitGuard) Enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancelled {
		return false
	}
	g.committing = true
	return true
}

func (g *CommitGuard) Leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.committing = false
}

func (g *CommitGuard) tryFence() (mayCancel bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.committing {
		return false
	}
	g.cancelled = true
	return true
}
