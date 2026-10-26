package buildinfo

import "runtime/debug"

var (
	Version           = "dev"
	Channel           = "unknown"
	BuildTime         = "unknown"
	SourceFingerprint = "unknown"
	GitCommit         = "unknown"
	SnapshotFormat    = "unknown"
	ReleaseSource     = "https://github.com/victorarias/attn/releases/download"
	RepositoryPath    = ""
	Dirty             bool
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if GitCommit == "unknown" {
				GitCommit = setting.Value
			}
		case "vcs.modified":
			Dirty = setting.Value == "true"
		}
	}
}
