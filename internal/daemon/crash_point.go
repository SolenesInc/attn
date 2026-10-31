package daemon

const (
	crashAfterSeedArtifactStaged    = "seed-artifact-staged"
	crashAfterSeedArtifactInstalled = "seed-artifact-installed"
	crashAfterMergePersisted        = "pull-request-merge-persisted"
	crashAfterWorktreeJournaled     = "delegation-worktree-journaled"
	crashAfterWorktreeOwned         = "delegation-worktree-owned"
	crashAfterGardenAdvice          = "garden-advice-received"
	crashBeforeLinkRecorded         = "pull-request-link-saved-before-record"
	crashBeforeTeardownKill         = "session-teardown-before-kill"
	crashAfterSuccessionCommitted   = "succession-committed"
)
