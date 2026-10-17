package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
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

func TestNamedInstancesPollTheMockGitHub(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ATTN_INSTANCE", "dev")
	t.Setenv("ATTN_GITHUB_POLLING", "")
	t.Setenv("ATTN_MOCK_GH_URL", "http://127.0.0.1:1")
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		if offReason := protocol.Deref(w.App().Initial.GithubPollingOffReason); offReason != "" {
			t.Errorf("the app is told polling is off because %q; want the mock GitHub polled", offReason)
		}
	})
}

func TestPRPollingStartsWhenGitHubBecomesAvailable(t *testing.T) {
	for _, availableAtStart := range []bool{false, true} {
		name := "host discovered after startup"
		if availableAtStart {
			name = "host available at startup"
		}
		t.Run(name, func(t *testing.T) {
			gh := mockserver.New()
			t.Cleanup(gh.Close)
			gh.Config.SetKeepAlivesEnabled(false)
			gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "reviewer"})
			t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
			t.Setenv("ATTN_MOCK_GH_TOKEN", "")
			if availableAtStart {
				t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
			}
			inBubble(t, func(t *testing.T, w *world) {
				w.finishStartupWork()
				app := w.App()
				if !availableAtStart {
					if len(app.Initial.GithubHosts) != 0 || len(app.Initial.Prs) != 0 {
						t.Fatalf("offline startup = hosts %v, PRs %+v; want neither", app.Initial.GithubHosts, app.Initial.Prs)
					}
					w.advance(5 * time.Minute)
					if requests := gh.Requests(); len(requests) != 0 {
						t.Fatalf("GitHub received requests without a host: %+v", requests)
					}
					t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
					w.advance(5 * time.Minute)
					testworld.Await(app, protocol.EventGitHubHostsUpdated, func(m protocol.GitHubHostsUpdatedMessage) bool {
						return len(m.GithubHosts) == 1
					})
					w.advance(90 * time.Second)
				}
				if len(app.Initial.Prs) == 0 {
					testworld.Await(app, protocol.EventPRsUpdated, func(m protocol.PRsUpdatedMessage) bool {
						return len(m.Prs) == 1 && m.Prs[0].Title == "Add checkout"
					})
				}
				if prs := queryPRs(t, w.Client(), ""); len(prs) != 1 || prs[0].Title != "Add checkout" {
					t.Fatalf("dashboard = %+v, want the automatically polled pull request", prs)
				}
			})
		})
	}
}
