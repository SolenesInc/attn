package fakeagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	codexVersionFile = "codex-version"
	codexLatestFile  = "codex-latest"
	codexBaseVersion = "1.0.0"
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

func (k *Kit) InstallStandaloneCodex(root, version string) string {
	k.t.Helper()
	self, err := os.Executable()
	if err != nil {
		k.t.Fatal(err)
	}
	encoded, err := json.Marshal(k.cfg)
	if err != nil {
		k.t.Fatal(err)
	}
	release := filepath.Join(root, "releases", version)
	bin := filepath.Join(release, "bin")
	mustInstall(k.t, bin, nil, map[string]string{configName: string(encoded), codexVersionFile: version + "\n"})
	if err := os.Link(self, filepath.Join(bin, string(Codex))); err != nil {
		k.t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(release, current+".next"); err != nil {
		k.t.Fatal(err)
	}
	if err := os.Rename(current+".next", current); err != nil {
		k.t.Fatal(err)
	}
	return filepath.Join(current, "bin", string(Codex))
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
