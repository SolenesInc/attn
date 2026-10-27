package fakeagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/mod/semver"
)

const (
	codexVersionFile    = "codex-version"
	codexLatestFile     = "codex-latest"
	codexStartErrorFile = "codex-start-error"
	codexNpmNativeFile  = "codex-npm-native"
	codexUpdateFailFile = "codex-update-error"
	codexBaseVersion    = "1.0.0"
	miseName            = "mise"
	miseVersion         = "2026.9.4 macos-arm64 (2026-09-30)"
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

const codexUpdatePrompt = "✨ Update available!"

func offerCodexUpdate(cfg config, term *terminal, overrides []string) error {
	installed, latest := installedCodexVersion(), latestCodexVersion(cfg)
	if slices.Contains(overrides, "check_for_update_on_startup=false") || semver.Compare("v"+latest, "v"+installed) <= 0 {
		return nil
	}
	term.print(codexUpdatePrompt + " " + installed + " -> " + latest)
	term.print("› 1. Update now")
	term.print("  2. Skip")
	if err := term.awaitEnter(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
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

func codexUpdate(cfg config) int {
	if line := readVersionFile(filepath.Join(programDir(), codexUpdateFailFile), ""); line != "" {
		fmt.Fprintln(os.Stderr, line)
		return 1
	}
	latest := latestCodexVersion(cfg)
	if err := os.WriteFile(filepath.Join(programDir(), codexVersionFile), []byte(latest+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "fake codex update: %v\n", err)
		return 1
	}
	fmt.Println("Codex updated to " + latest)
	return 0
}

func (k *Kit) FailCodexUpdate(line string) {
	k.t.Helper()
	path := filepath.Join(k.cfg.Bin, codexUpdateFailFile)
	if line == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			k.t.Fatal(err)
		}
		return
	}
	k.writeVersion(path, line)
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
	self, encoded := k.program()
	mustInstall(k.t, dir, map[string]string{string(Codex): self}, map[string]string{configName: encoded, codexVersionFile: version + "\n"})
	return filepath.Join(dir, string(Codex))
}

func (k *Kit) InstallStandaloneCodex(root, version string) string {
	k.t.Helper()
	self, encoded := k.program()
	release := filepath.Join(root, "releases", version)
	bin := filepath.Join(release, "bin")
	mustInstall(k.t, bin, nil, map[string]string{configName: encoded, codexVersionFile: version + "\n"})
	k.link(self, filepath.Join(bin, string(Codex)))
	current := filepath.Join(root, "current")
	if err := os.Symlink(release, current+".next"); err != nil {
		k.t.Fatal(err)
	}
	if err := os.Rename(current+".next", current); err != nil {
		k.t.Fatal(err)
	}
	return filepath.Join(current, "bin", string(Codex))
}

func (k *Kit) InstallMiseCodex(root, version string) string {
	k.t.Helper()
	self, encoded := k.program()
	mustInstall(k.t, root, nil, map[string]string{configName: encoded})
	mise := filepath.Join(root, miseName)
	k.link(self, mise)
	shims := filepath.Join(root, "shims")
	mustInstall(k.t, shims, map[string]string{string(Codex): mise}, map[string]string{configName: encoded, codexVersionFile: version + "\n"})
	return filepath.Join(shims, string(Codex))
}

func runMise(cfg config) int {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(miseVersion)
		return 0
	}
	return tripwire(cfg, miseName, "mise ran with Codex's arguments; its shims reach Codex only through the name codex")
}

func (k *Kit) program() (self, encoded string) {
	k.t.Helper()
	self, err := os.Executable()
	if err != nil {
		k.t.Fatal(err)
	}
	data, err := json.Marshal(k.cfg)
	if err != nil {
		k.t.Fatal(err)
	}
	return self, string(data)
}

func (k *Kit) link(self, path string) {
	k.t.Helper()
	if err := os.Link(self, path); err != nil {
		k.t.Fatal(err)
	}
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
