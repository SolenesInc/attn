package daemon_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	hostA = "a.github.example"
	hostB = "b.github.example"
)

type twoHostGitHub struct {
	t   *testing.T
	dir string

	mu     sync.Mutex
	titles map[string]string
}

func newTwoHostGitHub(t *testing.T) *twoHostGitHub {
	t.Helper()
	gh := &twoHostGitHub{t: t, dir: t.TempDir(), titles: map[string]string{hostA: "first", hostB: "first"}}
	script := `#!/bin/sh
case "$1 $2" in
  "--version "*) echo 'gh version 2.98.0 (2026-08-20)' ;;
  "auth status") /bin/cat "$FAKE_GH_DIR/status.json" ;;
  "auth token") [ -e "$FAKE_GH_DIR/no-token-$4" ] && exit 1; echo 'test-token' ;;
esac
`
	if err := os.WriteFile(filepath.Join(gh.dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gh.reportHosts(map[string]string{hostA: "success", hostB: "success"})
	t.Setenv("FAKE_GH_DIR", gh.dir)
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_GITHUB_POLLING", "on")
	t.Setenv("PATH", gh.dir+string(os.PathListSeparator)+ghWarningPath(t, ""))
	original := http.DefaultTransport
	http.DefaultTransport = gh
	t.Cleanup(func() { http.DefaultTransport = original })
	return gh
}

func (gh *twoHostGitHub) reportHosts(states map[string]string) {
	gh.t.Helper()
	hosts := map[string][]map[string]any{}
	for host, state := range states {
		hosts[host] = []map[string]any{{"state": state, "active": true, "login": "ada"}}
	}
	status, err := json.Marshal(map[string]any{"hosts": hosts})
	if err != nil {
		gh.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gh.dir, "status.json"), status, 0o644); err != nil {
		gh.t.Fatal(err)
	}
}

func (gh *twoHostGitHub) refuseToken(host string) {
	gh.t.Helper()
	if err := os.WriteFile(filepath.Join(gh.dir, "no-token-"+host), nil, 0o644); err != nil {
		gh.t.Fatal(err)
	}
}

func (gh *twoHostGitHub) grantToken(host string) {
	gh.t.Helper()
	if err := os.Remove(filepath.Join(gh.dir, "no-token-"+host)); err != nil {
		gh.t.Fatal(err)
	}
}

func (gh *twoHostGitHub) retitle(host, title string) {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	gh.titles[host] = title
}

func (gh *twoHostGitHub) closePR(host string) { gh.retitle(host, "") }

func (gh *twoHostGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if host != hostA && host != hostB {
		return nil, fmt.Errorf("the fake GitHub has no host %q", host)
	}
	body := `{}`
	if strings.HasSuffix(req.URL.Path, "/search/issues") {
		items := []any{}
		gh.mu.Lock()
		title := gh.titles[host]
		gh.mu.Unlock()
		if title != "" && strings.Contains(req.URL.Query().Get("q"), "author:@me") {
			items = append(items, map[string]any{
				"number":         1,
				"title":          title,
				"html_url":       "https://" + host + "/acme/demo/pull/1",
				"repository_url": "https://" + host + "/api/v3/repos/acme/demo",
				"user":           map[string]string{"login": "ada"},
			})
		}
		encoded, err := json.Marshal(map[string]any{"items": items})
		if err != nil {
			return nil, err
		}
		body = string(encoded)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func hostPR(t *testing.T, prs []protocol.PR, host string) protocol.PR {
	t.Helper()
	i := slices.IndexFunc(prs, func(pr protocol.PR) bool { return pr.Host == host })
	if i < 0 {
		t.Fatalf("no pull request of %s among %+v", host, prs)
	}
	return prs[i]
}

func worldWithBothHostsMuted(t *testing.T) *world {
	t.Helper()
	w := &world{World: prepareWorld(t)}
	w.start()
	app := w.App()
	if len(app.Initial.GithubHosts) != 2 {
		testworld.Await(app, protocol.EventGitHubHostsUpdated, func(m protocol.GitHubHostsUpdatedMessage) bool { return len(m.GithubHosts) == 2 })
	}
	refreshPRs(t, app)
	cli := w.Client()
	for _, pr := range queryPRs(t, cli, "") {
		if err := cli.ToggleMutePR(pr.ID); err != nil {
			t.Fatal(err)
		}
	}
	prs := queryPRs(t, cli, "")
	if len(prs) != 2 || !prs[0].Muted || !prs[1].Muted {
		t.Fatalf("seeded pull requests %+v, want one muted on each host", prs)
	}
	return w
}

func assertMuted(t *testing.T, prs []protocol.PR, hosts ...string) {
	t.Helper()
	if len(prs) != len(hosts) {
		t.Fatalf("pull requests %+v, want one on each of %v", prs, hosts)
	}
	for _, host := range hosts {
		if pr := hostPR(t, prs, host); !pr.Muted {
			t.Errorf("the pull request of %s lost its mute: %+v", host, pr)
		}
	}
}

func otherHost(host string) string {
	if host == hostA {
		return hostB
	}
	return hostA
}

func TestAPullRequestListChangeDuringHostDiscoveryKeepsTheMutesOfHostsNotYetFound(t *testing.T) {
	refresh := func(t *testing.T, app *testworld.Peer) { refreshPRs(t, app) }
	for _, entry := range []struct {
		name              string
		beforeFirstScreen bool
		change            func(t *testing.T, app *testworld.Peer)
	}{
		{name: "a refresh after the first screen", change: refresh},
		{name: "a reconnecting app's refresh before its first screen", beforeFirstScreen: true, change: refresh},
		{name: "the refresh that follows an approval", change: func(t *testing.T, app *testworld.Peer) {
			found := app.Initial.GithubHosts[0]
			id := hostPR(t, app.Initial.Prs, found).ID
			app.Send(protocol.ApprovePRMessage{Cmd: protocol.CmdApprovePR, ID: id})
			testworld.Await(app, protocol.EventPRsUpdated, func(e protocol.WebSocketEvent) bool {
				return slices.ContainsFunc(e.Prs, func(pr protocol.PR) bool { return pr.ID == id && pr.Title == "second" })
			})
		}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			gh := newTwoHostGitHub(t)
			w := worldWithBothHostsMuted(t)
			gh.retitle(hostA, "second")
			gh.retitle(hostB, "second")

			oneRegistered := w.PauseAt(pausepoint.DaemonGitHubHostRegistered)
			var recovery *testworld.Pause
			if entry.beforeFirstScreen {
				recovery = w.PauseAt(pausepoint.DaemonStartupRecovery)
			}
			w.restart()
			oneRegistered.Await()
			var app *testworld.Peer
			if entry.beforeFirstScreen {
				recovery.Await()
				app = w.ConnectApp()
			} else {
				app = w.App()
				assertMuted(t, app.Initial.Prs, hostA, hostB)
			}

			entry.change(t, app)

			during := queryPRs(t, w.Client(), "")
			assertMuted(t, during, hostA, hostB)
			if a, b := hostPR(t, during, hostA).Title, hostPR(t, during, hostB).Title; a == b {
				t.Errorf("titles %q and %q, want only the found host's pull request refreshed", a, b)
			}

			if recovery != nil {
				recovery.Release()
				testworld.Await[protocol.InitialStateMessage](app, protocol.EventInitialState, nil)
			}
			oneRegistered.Release()
			testworld.Await(app, protocol.EventGitHubHostsUpdated, func(m protocol.GitHubHostsUpdatedMessage) bool { return len(m.GithubHosts) == 2 })
			refreshPRs(t, app)
			after := queryPRs(t, w.Client(), "")
			assertMuted(t, after, hostA, hostB)
			if a, b := hostPR(t, after, hostA).Title, hostPR(t, after, hostB).Title; a != "second" || b != "second" {
				t.Errorf("once both hosts are found the titles are %q and %q, want both refreshed", a, b)
			}
		})
	}
}

func TestARefreshDuringHostDiscoveryRemovesAClosedPullRequestOfAFoundHostOnly(t *testing.T) {
	gh := newTwoHostGitHub(t)
	w := worldWithBothHostsMuted(t)

	oneRegistered := w.PauseAt(pausepoint.DaemonGitHubHostRegistered)
	w.restart()
	oneRegistered.Await()
	app := w.App()
	found := app.Initial.GithubHosts[0]
	gh.closePR(found)
	refreshPRs(t, app)
	assertMuted(t, queryPRs(t, w.Client(), ""), otherHost(found))
	oneRegistered.Release()
}

func TestAHostThatCannotBeUsedForNowKeepsItsPullRequestsAndMutes(t *testing.T) {
	for _, outage := range []struct {
		name          string
		begin, finish func(gh *twoHostGitHub)
	}{
		{
			name:   "gh refuses its token",
			begin:  func(gh *twoHostGitHub) { gh.refuseToken(hostB) },
			finish: func(gh *twoHostGitHub) { gh.grantToken(hostB) },
		},
		{
			name: "gh reports it timed out",
			begin: func(gh *twoHostGitHub) {
				gh.reportHosts(map[string]string{hostA: "success", hostB: "timeout"})
			},
			finish: func(gh *twoHostGitHub) {
				gh.reportHosts(map[string]string{hostA: "success", hostB: "success"})
			},
		},
	} {
		t.Run(outage.name, func(t *testing.T) {
			gh := newTwoHostGitHub(t)
			w := worldWithBothHostsMuted(t)

			w.stop()
			outage.begin(gh)
			gh.retitle(hostA, "polled")
			discovery := w.PauseAt(pausepoint.DaemonGitHubHostDiscovery)
			w.start()
			discovery.Await()
			app := w.App()
			discovery.Release()
			testworld.Await(app, protocol.EventGitHubHostsUpdated, func(m protocol.GitHubHostsUpdatedMessage) bool {
				return slices.Equal(m.GithubHosts, []string{hostA})
			})
			testworld.Await(app, protocol.EventPRsUpdated, func(e protocol.WebSocketEvent) bool {
				return slices.ContainsFunc(e.Prs, func(pr protocol.PR) bool { return pr.Host == hostA && pr.Title == "polled" })
			})
			assertMuted(t, queryPRs(t, w.Client(), ""), hostA, hostB)

			w.stop()
			outage.finish(gh)
			w.start()
			discovery.Await()
			app = w.App()
			discovery.Release()
			if len(app.Initial.GithubHosts) != 2 {
				testworld.Await(app, protocol.EventGitHubHostsUpdated, func(m protocol.GitHubHostsUpdatedMessage) bool { return len(m.GithubHosts) == 2 })
			}
			refreshPRs(t, app)
			assertMuted(t, queryPRs(t, w.Client(), ""), hostA, hostB)
		})
	}
}

func TestSigningOutOfAHostRemovesItsPullRequests(t *testing.T) {
	gh := newTwoHostGitHub(t)
	w := worldWithBothHostsMuted(t)

	w.stop()
	gh.reportHosts(map[string]string{hostA: "success"})
	discovery := w.PauseAt(pausepoint.DaemonGitHubHostDiscovery)
	w.start()
	discovery.Await()
	app := w.App()
	assertMuted(t, app.Initial.Prs, hostA, hostB)
	discovery.Release()
	testworld.Await(app, protocol.EventPRsUpdated, func(e protocol.WebSocketEvent) bool {
		return !slices.ContainsFunc(e.Prs, func(pr protocol.PR) bool { return pr.Host == hostB })
	})
	assertMuted(t, queryPRs(t, w.Client(), ""), hostA)
}
