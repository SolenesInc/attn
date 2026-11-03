package buildinfo

import (
	"runtime/debug"
	"strings"
)

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
	vcsModified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if GitCommit == "unknown" {
				GitCommit = setting.Value
			}
		case "vcs.modified":
			vcsModified = setting.Value == "true"
		}
	}
	Dirty = shippedSourceDiffersFromCommit(SourceFingerprint, vcsModified)
}

func shippedSourceDiffersFromCommit(fingerprint string, vcsModified bool) bool {
	switch {
	case strings.HasPrefix(fingerprint, "tree:"):
		return true
	case strings.HasPrefix(fingerprint, "git:"):
		return false
	default:
		return vcsModified
	}
}
