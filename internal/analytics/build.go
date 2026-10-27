package analytics

import (
	"runtime"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
)

type Build = protocol.AnalyticsBuild

func CurrentBuild() Build {
	return Build{
		Version:     buildinfo.Version,
		Commit:      buildinfo.GitCommit,
		Channel:     buildinfo.Channel,
		BuildTime:   buildinfo.BuildTime,
		Fingerprint: buildinfo.SourceFingerprint,
		Protocol:    protocol.ProtocolVersion,
		Os:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Dirty:       buildinfo.Dirty,
	}
}
