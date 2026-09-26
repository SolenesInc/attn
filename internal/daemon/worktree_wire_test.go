package daemon_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheWorktreeLogPagesNewestFirstPerRepository(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop, ledger := newRepo(t, "shop"), newRepo(t, "ledger")
	var removed []string
	for _, wt := range []struct{ repo, branch string }{
		{shop, "feat-one"}, {shop, "feat-two"}, {shop, "feat-three"}, {ledger, "feat-other"},
	} {
		path := createWorktree(t, app, wt.repo, wt.branch)
		if err := cli.DeleteWorktree(path, false); err != nil {
			t.Fatalf("delete %s: %v", path, err)
		}
		removed = append(removed, path)
	}

	page, err := cli.WorktreeSweepLog(shop, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := sweptPaths(page.Entries); !slices.Equal(got, []string{removed[2], removed[1]}) || page.Omitted != 1 {
		t.Errorf("shop's page of 2 = %v with %d omitted, want the two newest shop removals and one omitted", got, page.Omitted)
	}
	all, err := cli.WorktreeSweepLog("", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := sweptPaths(all.Entries); !slices.Equal(got, []string{removed[3], removed[2], removed[1], removed[0]}) || all.Omitted != 0 {
		t.Errorf("the whole log = %v with %d omitted, want every removal newest first", got, all.Omitted)
	}
}

func TestAMergedPullRequestKeepsItsWorktreeMergedAfterGitHubStopsListingIt(t *testing.T) {
	pulls := newMergedPullRequests(t)
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop := newRepo(t, "shop")
	runGit(t, shop, "remote", "add", "origin", "https://github.test/acme/shop.git")
	runGit(t, shop, "update-ref", "refs/remotes/origin/main", "HEAD")
	path := createWorktree(t, app, shop, "feat-login")
	head := commitFile(t, path, "login.go", "package login\n")
	scratch := filepath.Join(path, "scratch.txt")
	if err := os.WriteFile(scratch, []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pulls.list(mergedPullRequest{Number: 7, MergedAt: "2026-08-06T10:00:00Z", Head: ref{Ref: "feat-login", SHA: head}, Base: ref{Ref: "main"}})

	refreshWorktrees(t, cli)
	testworld.Await(app, protocol.EventWorktreeStateChanged, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == path && protocol.Deref(e.Worktrees[0].MergedSignal) == "pull_request"
	})

	pulls.list()
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	refreshWorktrees(t, cli)
	swept := testworld.Await(app, protocol.EventWorktreeSwept, func(e protocol.WebSocketEvent) bool {
		return e.SweepEntry != nil && e.SweepEntry.Path == path
	}).SweepEntry
	if swept.Action != "removed" || !strings.Contains(protocol.Deref(swept.Reason), "pull_request") {
		t.Errorf("the sweep recorded %s (%s), want it removed as merged by its pull request", swept.Action, protocol.Deref(swept.Reason))
	}
	if pulls.servedEmpty() == 0 {
		t.Error("the second refresh never asked GitHub, so it proves nothing about a PR that stopped being listed")
	}
}

type ref struct {
	Ref string `json:"ref"`
	SHA string `json:"sha,omitempty"`
}

type mergedPullRequest struct {
	Number   int    `json:"number"`
	MergedAt string `json:"merged_at"`
	Head     ref    `json:"head"`
	Base     ref    `json:"base"`
}

type mergedPullRequests struct {
	mu     sync.Mutex
	pulls  []mergedPullRequest
	empty  int
	server *httptest.Server
}

func newMergedPullRequests(t *testing.T) *mergedPullRequests {
	t.Helper()
	m := &mergedPullRequests{}
	m.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/repos/acme/shop/pulls" {
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		m.mu.Lock()
		pulls := m.pulls
		if len(pulls) == 0 {
			m.empty++
		}
		m.mu.Unlock()
		_ = json.NewEncoder(rw).Encode(append([]mergedPullRequest{}, pulls...))
	}))
	t.Cleanup(m.server.Close)
	t.Setenv("ATTN_MOCK_GH_URL", m.server.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.test")
	return m
}

func (m *mergedPullRequests) list(pulls ...mergedPullRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pulls = pulls
}

func (m *mergedPullRequests) servedEmpty() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.empty
}

func refreshWorktrees(t *testing.T, cli *client.Client) {
	t.Helper()
	result, err := cli.WorktreeRefresh()
	if err != nil || !result.Queued {
		t.Fatalf("worktree refresh = %+v, %v", result, err)
	}
}

func createWorktree(t *testing.T, app *testworld.Peer, repo, branch string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"--"+branch)
	result := testworld.Request(app, protocol.CreateWorktreeMessage{
		Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: branch, Path: protocol.Ptr(path),
	}, protocol.EventCreateWorktreeResult, func(r protocol.CreateWorktreeResultMessage) bool {
		return protocol.Deref(r.Path) == path
	})
	if !result.Success {
		t.Fatalf("create worktree %s: %s", branch, protocol.Deref(result.Error))
	}
	return path
}

func newRepo(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "README.md", name+"\n")
	return repo
}

func commitFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", "add "+name)
	return strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=attn", "-c", "user.email=attn@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func sweptPaths(entries []protocol.WorktreeSweepEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return paths
}
