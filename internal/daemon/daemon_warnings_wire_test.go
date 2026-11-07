package daemon_test

import (
	"strings"
	"testing"
	"time"
)

func TestTheAppIsToldEveryGitHubHostGHIsSignedInTo(t *testing.T) {
	useSignedInGH(t)
	hosts := awaitGitHubHosts(newWorld(t).App())
	if got := strings.Join(hosts, ","); got != "ghe.example.test,github.example.test" {
		t.Fatalf("the app is told of GitHub hosts %q, want both hosts gh is signed in to", got)
	}
}

func TestAWarningRaisedAgainOnEachRefreshIsShownOnce(t *testing.T) {
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_GITHUB_POLLING", "on")
	t.Setenv("PATH", ghWarningPath(t, ""))
	inBubble(t, func(t *testing.T, w *world) {
		w.advance(16 * time.Minute)
		missing := 0
		for _, warning := range w.App().Initial.Warnings {
			if warning.Code == "gh_not_installed" {
				missing++
			}
		}
		if missing != 1 {
			t.Fatalf("after three GitHub host refreshes the app shows %d gh_not_installed warnings, want 1", missing)
		}
	})
}
