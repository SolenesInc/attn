package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/outpostbuild"
)

func (d *Daemon) RestartRequested() <-chan struct{} { return d.restartRequested }

func (d *Daemon) outpostUpdateServer() *bridge.UpdateServer {
	executable, err := os.Executable()
	if err != nil {
		d.logf("outpost executable: %v", err)
		return nil
	}
	return &bridge.UpdateServer{
		Target:  bridge.UpdateTarget{Platform: runtime.GOOS + "/" + runtime.GOARCH, Executable: executable},
		Receive: d.installOutpostBuild,
		Restart: func() { d.restartOnce.Do(func() { close(d.restartRequested) }) },
	}
}

func (d *Daemon) installOutpostBuild(ctx context.Context, header bridge.UpdateHeader, compressed io.Reader, verifying func()) error {
	r := &d.outpost
	r.mu.Lock()
	if r.updating {
		r.mu.Unlock()
		return fmt.Errorf("an outpost update is already in progress")
	}
	r.updating = true
	r.mu.Unlock()
	installed := false
	defer func() {
		if !installed {
			r.mu.Lock()
			r.updating = false
			r.mu.Unlock()
		}
	}()
	if os.Getenv("INVOCATION_ID") == "" {
		return fmt.Errorf("outpost is not running under systemd (INVOCATION_ID is empty); install the documented user unit")
	}
	if parseBooleanSetting(d.daemonSetting(SettingSharedPTYHostEnabled)) {
		return fmt.Errorf("outpost has pty_shared_host_enabled on; this update carries attn only and would leave attn-pty-host on the old build")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := outpostbuild.CheckInstallTarget(config.InstanceLabel(), executable, home); err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(executable), ".attn-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	reader, err := zstd.NewReader(compressed, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer reader.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(staged, hash), io.LimitReader(reader, header.Size+1))
	if err != nil {
		return fmt.Errorf("receive binary: %w", err)
	}
	if size != header.Size {
		return fmt.Errorf("update size=%d, received %d bytes", header.Size, size)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != header.SHA256 {
		return fmt.Errorf("update sha256=%s, received %s", header.SHA256, actual)
	}
	if err := staged.Chmod(0755); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	verifying()
	probe := func(args ...string) ([]byte, error) {
		probeCtx, cancel := context.WithTimeout(ctx, bridge.LostAfter)
		defer cancel()
		out, err := exec.CommandContext(probeCtx, staged.Name(), args...).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("staged binary %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return out, nil
	}
	out, err := probe("--build-info-json")
	if err != nil {
		return err
	}
	var info struct {
		Version     string `json:"version"`
		Fingerprint string `json:"sourceFingerprint"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return fmt.Errorf("staged build info: %w", err)
	}
	out, err = probe("--protocol-version")
	if err != nil {
		return err
	}
	actual := bridge.Build{Version: info.Version, SourceFingerprint: info.Fingerprint, ProtocolVersion: strings.TrimSpace(string(out))}
	if actual != header.Build {
		return fmt.Errorf("staged build %s differs from home's build %s", actual, header.Build)
	}
	if _, err := probe("--check-schema", config.DBPath()); err != nil {
		return err
	}
	previous := filepath.Join(filepath.Dir(executable), "attn.previous")
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(executable, previous); err != nil {
		return fmt.Errorf("keep previous binary: %w", err)
	}
	if err := os.Rename(staged.Name(), executable); err != nil {
		return fmt.Errorf("replace outpost binary: %w", err)
	}
	installed = true
	return nil
}
