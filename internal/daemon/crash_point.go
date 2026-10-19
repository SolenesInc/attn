package daemon

const (
	crashAfterSeedArtifactStaged    = "seed-artifact-staged"
	crashAfterSeedArtifactInstalled = "seed-artifact-installed"
	crashAfterMergePersisted        = "pull-request-merge-persisted"
	crashAfterWorktreeJournaled     = "delegation-worktree-journaled"
	crashAfterWorktreeOwned         = "delegation-worktree-owned"
	crashAfterGardenAdvice          = "garden-advice-received"
)
