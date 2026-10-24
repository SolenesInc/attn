package analytics

import (
	"runtime"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
)

type Build struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Channel     string `json:"channel"`
	BuildTime   string `json:"build_time"`
	Fingerprint string `json:"fingerprint"`
	Protocol    string `json:"protocol"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Dirty       bool   `json:"dirty"`
	AppVersion  string `json:"app_version,omitempty"`
	AppChannel  string `json:"app_channel,omitempty"`
}

func CurrentBuild() Build {
	return Build{
		Version:     buildinfo.Version,
		Commit:      buildinfo.GitCommit,
		Channel:     buildinfo.Channel,
		BuildTime:   buildinfo.BuildTime,
		Fingerprint: buildinfo.SourceFingerprint,
		Protocol:    protocol.ProtocolVersion,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Dirty:       buildinfo.Dirty,
	}
}
