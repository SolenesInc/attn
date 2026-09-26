package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestNamedInstancesPollGitHubOnlyWhenOptedIn(t *testing.T) {
	for _, row := range []struct {
		name, instance, optIn string
		wantOffReason         string
	}{
		{"a named instance", "dev", "", "GitHub polling is off for instance dev. Start its daemon with ATTN_GITHUB_POLLING=on"},
		{"a named instance started with the opt-in", "dev", "on", ""},
		{"the unnamed instance", "", "", ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			t.Setenv("ATTN_MOCK_GH_URL", "")
			t.Setenv("ATTN_INSTANCE", row.instance)
			t.Setenv("ATTN_GITHUB_POLLING", row.optIn)
			inBubble(t, func(t *testing.T, w *world) {
				w.finishStartupWork()
				initial := w.App().Initial

				offReason := protocol.Deref(initial.GithubPollingOffReason)
				if row.wantOffReason == "" && offReason != "" || !strings.HasPrefix(offReason, row.wantOffReason) {
					t.Errorf("the app is told polling is off because %q; want %q", offReason, row.wantOffReason)
				}
				if len(initial.GithubHosts) != 0 {
					t.Errorf("the app sees GitHub hosts %v; want none without gh", initial.GithubHosts)
				}
				consultedGH := false
				for _, warning := range initial.Warnings {
					consultedGH = consultedGH || warning.Code == "gh_not_installed"
				}
				if wantConsulted := row.wantOffReason == ""; consultedGH != wantConsulted {
					t.Errorf("the daemon looked for gh: %v; want %v (warnings %+v)", consultedGH, wantConsulted, initial.Warnings)
				}
			})
		})
	}
}
