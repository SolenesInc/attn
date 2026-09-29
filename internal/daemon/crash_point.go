package daemon

import (
	"os"
	"strings"
	"syscall"
)

const (
	crashAfterSeedArtifactStaged    = "seed-artifact-staged"
	crashAfterSeedArtifactInstalled = "seed-artifact-installed"
	crashAfterMergePersisted        = "pull-request-merge-persisted"
	crashWhileWakingASnooze         = "snooze-wake-running"
	crashAfterWorktreeJournaled     = "delegation-worktree-journaled"
	crashAfterWorktreeOwned         = "delegation-worktree-owned"
)

func crashAt(point string) {
	if strings.TrimSpace(os.Getenv("ATTN_CRASH_AT")) != point {
		return
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
