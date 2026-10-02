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
	crashAfterGardenAdvice          = "garden-advice-received"
	crashAfterCodexReservation      = "codex-launch-reserved"
	crashAfterCodexNativeArchive    = "codex-owner-native-archived"
	crashAfterCodexClosePersisted   = "codex-owner-close-persisted"
	crashAfterCodexAttachView       = "codex-attach-view-persisted"
	crashAfterCodexViewRemoved      = "codex-view-removed"
	crashAfterCodexNameWritten      = "codex-name-written"
)

func crashAt(point string) {
	if strings.TrimSpace(os.Getenv("ATTN_CRASH_AT")) != point {
		return
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
