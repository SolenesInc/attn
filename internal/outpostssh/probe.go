package outpostssh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
)

type Probe struct {
	Platform          string
	Build             *bridge.Build
	KillUserProcesses bool
}

func KillUserProcesses(ctx context.Context) (bool, error) {
	out, err := run(ctx, execBusctl(ctx))
	if err != nil {
		return false, fmt.Errorf("cannot read logind KillUserProcesses: %w", err)
	}
	return parseKillUserProcesses(string(out))
}
func parseKillUserProcesses(value string) (bool, error) {
	switch strings.TrimSpace(value) {
	case "b false":
		return false, nil
	case "b true":
		return true, nil
	default:
		return false, fmt.Errorf("cannot read logind KillUserProcesses: %s", strings.TrimSpace(value))
	}
}
func (c *Client) Probe(ctx context.Context) (Probe, error) {
	out, err := c.Run(ctx, `uname -sm; if [ -x `+c.binaryPath()+` ]; then `+c.binaryPath()+` --build-info-json; else echo missing; fi; busctl get-property org.freedesktop.login1 /org/freedesktop/login1 org.freedesktop.login1.Manager KillUserProcesses`)
	if err != nil {
		return Probe{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 3 {
		return Probe{}, fmt.Errorf("unexpected outpost probe: %s", out)
	}
	p := Probe{}
	switch lines[0] {
	case "Linux x86_64":
		p.Platform = "linux/amd64"
	case "Linux aarch64", "Linux arm64":
		p.Platform = "linux/arm64"
	default:
		return p, fmt.Errorf("outpost platform %q is unsupported; need Linux amd64 or arm64", lines[0])
	}
	p.KillUserProcesses, err = parseKillUserProcesses(lines[len(lines)-1])
	if err != nil {
		return p, err
	}
	if p.KillUserProcesses {
		return p, fmt.Errorf("logind KillUserProcesses is on; outpost agents would die when ssh disconnects; turn KillUserProcesses off before adding this outpost")
	}
	buildJSON := strings.Join(lines[1:len(lines)-1], "\n")
	if buildJSON != "missing" {
		var b struct {
			Version           string `json:"version"`
			SourceFingerprint string `json:"sourceFingerprint"`
			ProtocolVersion   string `json:"protocolVersion"`
		}
		if err := json.Unmarshal([]byte(buildJSON), &b); err != nil {
			return p, err
		}
		p.Build = &bridge.Build{Version: b.Version, SourceFingerprint: b.SourceFingerprint, ProtocolVersion: b.ProtocolVersion}
	}
	return p, nil
}
