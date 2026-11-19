package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWorktreeStartsFromTheAdvertisedDefault(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for _, row := range []struct {
		name, branch, configured, want string
		remote, offline                bool
	}{
		{name: "local main", branch: "main", want: "main"},
		{name: "local custom branch", branch: "develop", want: "develop"},
		{name: "configured local branch", branch: "release/next", configured: "release/next", want: "release/next"},
		{name: "remote default", branch: "main", remote: true, want: "origin/main"},
		{name: "remote default with slash", branch: "release/next", remote: true, want: "origin/release/next"},
		{name: "offline cached default", branch: "main", remote: true, offline: true, want: "origin/main"},
		{name: "configured remote ref", branch: "release/next", configured: "origin/release/next", remote: true, want: "origin/release/next"},
	} {
		t.Run(row.name, func(t *testing.T) {
			repo := newRepo(t, "shop")
			runGit(t, repo, "branch", "-m", row.branch)
			wantCommit := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
			if row.configured != "" {
				runGit(t, repo, "config", "attn.baseBranch", row.configured)
			}
			if row.remote {
				remote := newRepo(t, "remote")
				runGit(t, remote, "branch", "-m", row.branch)
				runGit(t, repo, "remote", "add", "origin", remote)
				runGit(t, repo, "fetch", "origin")
				runGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+row.branch)
				wantCommit = strings.TrimSpace(runGit(t, repo, "rev-parse", row.want))
				latest := commitFile(t, remote, "fresh.txt", "latest remote commit\n")
				if row.offline {
					if err := os.Rename(remote, remote+"-offline"); err != nil {
						t.Fatal(err)
					}
				} else {
					wantCommit = latest
				}
			}
			if row.branch != "develop" {
				runGit(t, repo, "checkout", "-b", "topic")
				commitFile(t, repo, "topic.txt", "current checkout differs from the default\n")
			}
			info := testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: repo},
				protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
			if !info.Success || info.Info == nil || info.Info.DefaultBranch != row.want {
				t.Fatalf("repo info = %+v (%s); want default %s", info.Info, protocol.Deref(info.Error), row.want)
			}
			created := testworld.Request(app, protocol.CreateWorktreeMessage{
				Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "new-work", StartingFrom: protocol.Ptr(info.Info.DefaultBranch),
			}, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
			if !created.Success {
				t.Fatal(protocol.Deref(created.Error))
			}
			if got := strings.TrimSpace(runGit(t, protocol.Deref(created.Path), "rev-parse", "HEAD")); got != wantCommit {
				t.Errorf("created HEAD = %s; want %s from %s", got, wantCommit, row.want)
			}
		})
	}
}

func TestAWorktreeBelongsToTheRepositoryItWasCreatedFrom(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "container", "hurdy-gurdy")
	if err := os.MkdirAll(filepath.Join(main, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "init", "-b", "main")
	commitFile(t, main, "README.md", "hurdy-gurdy\n")
	runGit(t, main, "branch", "feature/fun")
	otherWorktree := main + "--feat-auto-bump-yt-dlp--fork-hurdy-gurdy"
	runGit(t, main, "worktree", "add", "-b", "feat/auto-bump-yt-dlp", otherWorktree)

	create := func(from, branch string) protocol.CreateWorktreeMessage {
		return protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: from, Branch: branch}
	}
	for _, row := range []struct {
		name  string
		cmd   any
		wants string
	}{
		{"its logical path", create(filepath.Join(root, "hurdy-gurdy"), "feat/logical"), main + "--feat-logical"},
		{"a subdirectory", create(filepath.Join(main, "app"), "feat/subdir"), main + "--feat-subdir"},
		{"another worktree", create(otherWorktree, "fork/fun"), main + "--fork-fun"},
		{"another worktree, onto an existing branch", protocol.CreateWorktreeFromBranchMessage{
			Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: otherWorktree, Branch: "feature/fun",
		}, main + "--feature-fun"},
	} {
		result := testworld.Request(app, row.cmd, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
		if !result.Success || protocol.Deref(result.Path) != row.wants {
			t.Errorf("creating a worktree from %s = %q (%s); want %s", row.name, protocol.Deref(result.Path), protocol.Deref(result.Error), row.wants)
			continue
		}
		if registered, err := cli.WorktreeList(main, 0); err != nil || !slices.Contains(worktreeCreatePaths(registered.Worktrees), row.wants) {
			t.Errorf("the worktree created from %s is not registered under the main checkout %s: %+v, %v", row.name, main, registered, err)
		}
		if _, err := os.Stat(filepath.Join(row.wants, "README.md")); err != nil {
			t.Errorf("the worktree created from %s is not a checkout: %v", row.name, err)
		}
	}
}

func TestAVanishedWorktreeLeavesLiveQueriesAndItsPlaceCanBeReused(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		repo := newRepo(t, "shop")
		vanished := createWorktree(t, app, repo, "feat-vanished")
		reused := createWorktree(t, app, repo, "feat-reused")
		for _, path := range []string{vanished, reused} {
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
		}

		if paths := worktreeCreatePaths(repoInfoWorktrees(app, repo)); len(paths) != 0 {
			t.Errorf("the repository query after the directories vanished = %v; want neither", paths)
		}
		if surface, err := cli.WorktreeList(repo, 0); err != nil || !slices.Equal(worktreeCreatePaths(surface.Worktrees), []string{reused, vanished}) {
			t.Errorf("the registry after a live query = %+v, %v; want both retained for the sweep", surface, err)
		}

		result := testworld.Request(app, protocol.CreateWorktreeFromBranchMessage{
			Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: "feat-reused", Path: protocol.Ptr(reused),
		}, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
		if !result.Success || protocol.Deref(result.Path) != reused {
			t.Fatalf("checking feat-reused out where its vanished worktree was = %q (%s); want %s", protocol.Deref(result.Path), protocol.Deref(result.Error), reused)
		}
		if _, err := os.Stat(filepath.Join(reused, "README.md")); err != nil {
			t.Errorf("the worktree was not recreated: %v", err)
		}
		refreshWorktrees(t, cli)
		synctest.Wait()
		if surface, err := cli.WorktreeList(repo, 0); err != nil || !slices.Equal(worktreeCreatePaths(surface.Worktrees), []string{reused}) {
			t.Errorf("the worktree surface after recreating = %+v, %v; want only %s", surface, err, reused)
		}
	})
}

func worktreeCreatePaths(worktrees []protocol.Worktree) []string {
	var paths []string
	for _, wt := range worktrees {
		paths = append(paths, wt.Path)
	}
	return paths
}

func TestOpeningAPullRequestNeverReusesACheckoutOfAnotherRepository(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	requested := newRepo(t, "shop")
	commitFile(t, requested, "repo.txt", "requested\n")
	runGit(t, requested, "branch", "release")
	unrelated := newRepo(t, "shop")
	commitFile(t, unrelated, "repo.txt", "unrelated\n")
	runGit(t, unrelated, "branch", "release")
	checkout := w.Path("projects/shop")
	runGit(t, "", "clone", unrelated, checkout)
	ensure := func(cloneURL string) protocol.WebSocketEvent {
		return testworld.Request(app, protocol.EnsureRepoMessage{Cmd: protocol.CmdEnsureRepo, TargetPath: checkout, CloneURL: cloneURL},
			protocol.EventEnsureRepoResult, func(r protocol.WebSocketEvent) bool { return protocol.Deref(r.TargetPath) == checkout })
	}

	refused := ensure(requested)
	if protocol.Deref(refused.Success) || !strings.Contains(protocol.Deref(refused.Error), checkout) || !strings.Contains(protocol.Deref(refused.Error), requested) {
		t.Fatalf("ensuring %s in a checkout of another repository = %+v; want a refusal naming the checkout and the requested repository", requested, refused)
	}
	if branches := strings.TrimSpace(runGit(t, checkout, "branch", "--list", "release")); branches != "" {
		t.Errorf("the refused checkout gained a release branch: %q", branches)
	}

	runGit(t, checkout, "remote", "add", "upstream", requested)
	if fork := ensure(requested); !protocol.Deref(fork.Success) || protocol.Deref(fork.Cloned) {
		t.Errorf("ensuring %s in a fork whose upstream is that repository = %+v; want the fork reused", requested, fork)
	}

	const hosted = "https://github.com/first/shop.git"
	runGit(t, checkout, "remote", "set-url", "upstream", "git@github.com:First/Shop.git")
	runGit(t, checkout, "config", "url."+requested+".insteadOf", "git@github.com:First/Shop.git")
	if rewritten := ensure(hosted); !protocol.Deref(rewritten.Success) || protocol.Deref(rewritten.Cloned) {
		t.Errorf("ensuring %s in a checkout whose upstream is its SSH address, rewritten by insteadOf = %+v; want the checkout reused", hosted, rewritten)
	}
}
