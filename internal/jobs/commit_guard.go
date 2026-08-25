package jobs

import "sync"

// Enter immediately before the single durable write and Leave by defer after; between them
// Cancel waits instead of cancelling. Enter returning false means the run was already fenced.
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

// Called by the runner Cancel: true means the context may be cancelled, false
// means a committing run the caller must wait for.
func (g *CommitGuard) tryFence() (mayCancel bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.committing {
		return false
	}
	g.cancelled = true
	return true
}
