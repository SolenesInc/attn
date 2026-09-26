package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	attngithub "github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
)

func TestTheAppIsWarnedWhenGHIsMissingOrTooOldToMonitorPRs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ghVersion string
		code      string
		names     []string
	}{
		{name: "missing", code: "gh_not_installed", names: []string{"not installed", attngithub.InstallHint()}},
		{name: "too old", ghVersion: "gh version 2.45.0 (2024-01-01)", code: "gh_version_too_old", names: []string{"2.45.0", "2.81.0", attngithub.UpgradeHint()}},
		{name: "unparsable", ghVersion: "gh: something unexpected", code: "gh_version_too_old", names: []string{"2.81.0", attngithub.UpgradeHint()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ATTN_MOCK_GH_URL", "")
			t.Setenv("ATTN_GITHUB_POLLING", "on")
			t.Setenv("PATH", ghWarningPath(t, tc.ghVersion))
			inBubble(t, func(t *testing.T, w *world) {
				w.advance(0)
				warnings := w.App().Initial.Warnings
				i := slices.IndexFunc(warnings, func(warning protocol.DaemonWarning) bool { return warning.Code == tc.code })
				if i < 0 {
					t.Fatalf("the app's warnings are %+v, want one coded %s", warnings, tc.code)
				}
				for _, want := range tc.names {
					if !strings.Contains(warnings[i].Message, want) {
						t.Errorf("the %s warning %q does not name %q", tc.code, warnings[i].Message, want)
					}
				}
			})
		})
	}
}

func ghWarningPath(t *testing.T, ghVersion string) string {
	t.Helper()
	var dirs []string
	if ghVersion != "" {
		bin := t.TempDir()
		script := "#!/bin/sh\necho '" + ghVersion + "'\n"
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, bin)
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "gh")); err != nil {
			dirs = append(dirs, dir)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}
