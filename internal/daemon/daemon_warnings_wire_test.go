package daemon_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTheAppIsToldWhichDaemonInstanceItReached(t *testing.T) {
	w := newWorld(t)
	resp, err := http.Get("http://" + w.WSAddr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var health struct {
		DaemonInstanceID string `json:"daemon_instance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if got := protocol.Deref(w.App().Initial.DaemonInstanceID); got == "" || got != health.DaemonInstanceID {
		t.Fatalf("the app is told it reached daemon instance %q, want the running one %q", got, health.DaemonInstanceID)
	}
}

func TestTheAppIsToldEveryGitHubHostGHIsSignedInTo(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "--version "*) echo 'gh version 2.98.0 (2026-08-20)' ;;
  "auth status") echo '{"hosts":{"ghe.example.test":[{"state":"success","active":true,"login":"ada"}],"github.example.test":[{"state":"success","active":true,"login":"ada"}]}}' ;;
  "auth token") echo 'test-token' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_GITHUB_POLLING", "on")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+ghWarningPath(t, ""))
	inBubble(t, func(t *testing.T, w *world) {
		w.advance(0)
		hosts := w.App().Initial.GithubHosts
		slices.Sort(hosts)
		if got := strings.Join(hosts, ","); got != "ghe.example.test,github.example.test" {
			t.Fatalf("the app is told of GitHub hosts %q, want both hosts gh is signed in to", got)
		}
	})
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
