package fakeagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	codexVersionFile    = "codex-version"
	codexLatestFile     = "codex-latest"
	codexStartErrorFile = "codex-start-error"
	codexNpmNativeFile  = "codex-npm-native"
	codexBaseVersion    = "1.0.0"
)

func installedCodexVersion() string {
	return readVersionFile(filepath.Join(programDir(), codexVersionFile), codexBaseVersion)
}

func latestCodexVersion(cfg config) string {
	return readVersionFile(filepath.Join(cfg.ToolHome, codexLatestFile), installedCodexVersion())
}

func readVersionFile(path, fallback string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	if version := strings.TrimSpace(string(data)); version != "" {
		return version
	}
	return fallback
}

func codexPrintVersion() int {
	fmt.Println("codex-cli " + installedCodexVersion())
	return 0
}

func codexDoctor(cfg config) int {
	report := map[string]any{
		"schemaVersion": 1,
		"codexVersion":  installedCodexVersion(),
		"checks": map[string]any{
			"updates.status": map[string]any{
				"id":      "updates.status",
				"status":  "ok",
				"details": map[string]any{"latest version": latestCodexVersion(cfg)},
			},
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return 1
	}
	return 0
}

func (k *Kit) SetCodexVersion(version string) {
	k.t.Helper()
	k.writeVersion(filepath.Join(k.cfg.Bin, codexVersionFile), version)
}

func (k *Kit) RefuseCodexStart(line string) {
	k.t.Helper()
	path := filepath.Join(k.cfg.Bin, codexStartErrorFile)
	if line == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			k.t.Fatal(err)
		}
		return
	}
	k.writeVersion(path, line)
}

func (k *Kit) SetCodexLatest(version string) {
	k.t.Helper()
	k.writeVersion(filepath.Join(k.cfg.ToolHome, codexLatestFile), version)
}

func (k *Kit) InstallCodexAt(dir, version string) string {
	k.t.Helper()
	self, err := os.Executable()
	if err != nil {
		k.t.Fatal(err)
	}
	encoded, err := json.Marshal(k.cfg)
	if err != nil {
		k.t.Fatal(err)
	}
	mustInstall(k.t, dir, map[string]string{string(Codex): self}, map[string]string{configName: string(encoded), codexVersionFile: version + "\n"})
	return filepath.Join(dir, string(Codex))
}

func (s *CodexServer) Argv() []string { return s.fake.Argv }

func (k *Kit) writeVersion(path, version string) {
	k.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		k.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(version+"\n"), 0o644); err != nil {
		k.t.Fatal(err)
	}
}

func (k *Kit) InstallNpmCodex(root, version string) string {
	k.t.Helper()
	native, shim := filepath.Join(root, "vendor"), filepath.Join(root, "bin")
	if _, err := os.Stat(filepath.Join(shim, string(Codex))); err == nil {
		k.writeVersion(filepath.Join(native, codexVersionFile), version)
		return filepath.Join(shim, string(Codex))
	}
	installed := k.InstallCodexAt(shim, version)
	k.writeVersion(filepath.Join(shim, codexNpmNativeFile), k.InstallCodexAt(native, version))
	return installed
}

func runNpmShim(native string) int {
	cmd := exec.Command(native, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	terminations := make(chan os.Signal, 1)
	signal.Notify(terminations, syscall.SIGTERM)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "fake npm codex: %v\n", err)
		return 1
	}
	go func() {
		_ = cmd.Process.Signal(<-terminations)
	}()
	err := cmd.Wait()
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		if status, ok := exited.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return signalExitBase + int(status.Signal())
		}
		return exited.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}
