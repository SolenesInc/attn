package outpostssh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
)

type Probe struct {
	Platform          string
	Build             *bridge.Build
	Enrollment        enrollment.Status
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
	binary := c.binaryPath()
	out, err := c.Run(ctx, `uname -sm; if [ -x `+binary+` ]; then `+binary+` --build-info-json | tr -d "\n"; echo; `+binary+` enrollment status --json 2>/dev/null | tr -d "\n"; echo; else echo missing; echo; fi; busctl get-property org.freedesktop.login1 /org/freedesktop/login1 org.freedesktop.login1.Manager KillUserProcesses`)
	if err != nil {
		return Probe{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 4 {
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
	if buildJSON := lines[1]; buildJSON != "missing" {
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
	if status := lines[2]; status != "" {
		if err := json.Unmarshal([]byte(status), &p.Enrollment); err != nil {
			return p, fmt.Errorf("outpost enrollment status %q: %w", status, err)
		}
	}
	return p, nil
}
