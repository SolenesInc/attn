package github

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseGHVersionOutput(t *testing.T) {
	output := "gh version 2.81.0 (2025-01-01)\nhttps://github.com/cli/cli/releases\n"
	version, err := parseGHVersionOutput(output)
	if err != nil {
		t.Fatalf("parseGHVersionOutput error: %v", err)
	}
	if version != "2.81.0" {
		t.Fatalf("version = %q, want 2.81.0", version)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want int
	}{
		{"2.81.0", "2.81.0", 0},
		{"2.81.1", "2.81.0", 1},
		{"2.80.9", "2.81.0", -1},
		{"2.9.0", "2.10.0", -1},
		{"2.10.0", "2.9.0", 1},
		{"2.81.0-rc1", "2.81.0", 0},
		{"v2.81.0", "2.81.0", 0},
	}
	for _, tt := range tests {
		got, err := compareVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("compareVersions(%q,%q) error: %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Fatalf("compareVersions(%q,%q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestHintPerPlatform(t *testing.T) {
	const linuxDoc = "https://github.com/cli/cli/blob/trunk/docs/install_linux.md"
	tests := []struct {
		goos    string
		brewCmd string
		want    string
	}{
		{"darwin", "brew install gh", "Run: brew install gh"},
		{"darwin", "brew upgrade gh", "Run: brew upgrade gh"},
		{"linux", "brew install gh", "See " + linuxDoc},
		{"linux", "brew upgrade gh", "See " + linuxDoc},
	}
	for _, tt := range tests {
		if got := hint(tt.goos, tt.brewCmd); got != tt.want {
			t.Errorf("hint(%q, %q) = %q, want %q", tt.goos, tt.brewCmd, got, tt.want)
		}
	}
	if got := InstallHint(); got != hint(runtime.GOOS, "brew install gh") {
		t.Errorf("InstallHint() = %q, want hint for %q", got, runtime.GOOS)
	}
	if got := UpgradeHint(); got != hint(runtime.GOOS, "brew upgrade gh") {
		t.Errorf("UpgradeHint() = %q, want hint for %q", got, runtime.GOOS)
	}
	if runtime.GOOS != "darwin" && strings.Contains(InstallHint(), "brew") {
		t.Errorf("InstallHint() = %q on %s, must not name brew", InstallHint(), runtime.GOOS)
	}
}

func writeFakeGH(t *testing.T, dir, output string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := "#!/bin/sh\n" + output + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRequireGHVersionMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := RequireGHVersion("2.81.0")
	if err == nil {
		t.Fatal("RequireGHVersion with no gh on PATH: got nil error")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing gh error = %v, want wrapped exec.ErrNotFound", err)
	}
}

func TestRequireGHVersionTooOld(t *testing.T) {
	dir := t.TempDir()
	writeFakeGH(t, dir, `echo "gh version 2.45.0 (2023-01-25)"`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := RequireGHVersion("2.81.0")
	var tooOld *VersionTooOldError
	if !errors.As(err, &tooOld) {
		t.Fatalf("outdated gh error = %v (%T), want *VersionTooOldError", err, err)
	}
	if tooOld.Have != "2.45.0" || tooOld.Want != "2.81.0" {
		t.Fatalf("tooOld = %+v, want Have=2.45.0 Want=2.81.0", tooOld)
	}
	if !strings.Contains(err.Error(), "2.45.0") || !strings.Contains(err.Error(), UpgradeHint()) {
		t.Fatalf("tooOld message = %q, want found version and upgrade hint", err.Error())
	}
}

func TestRequireGHVersionOK(t *testing.T) {
	dir := t.TempDir()
	writeFakeGH(t, dir, `echo "gh version 2.98.0 (2026-08-20)"`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := RequireGHVersion("2.81.0"); err != nil {
		t.Fatalf("RequireGHVersion with new gh: %v", err)
	}
}
