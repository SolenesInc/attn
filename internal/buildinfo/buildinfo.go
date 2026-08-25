package buildinfo

var (
	Version           = "dev"
	BuildTime         = "unknown"
	SourceFingerprint = "unknown"
	GitCommit         = "unknown"
	// scripts/snapshot-format.sh derives this; "unknown" (a build that skipped the
	// ldflag) never matches a client and costs restore, never correctness.
	SnapshotFormat = "unknown"
)
