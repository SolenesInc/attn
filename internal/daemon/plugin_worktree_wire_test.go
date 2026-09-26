package daemon_test

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type pluginWorktreeCreate struct {
	MainRepo      string  `json:"main_repo"`
	Branch        string  `json:"branch"`
	StartingFrom  string  `json:"starting_from"`
	RequestedPath *string `json:"requested_path"`
}

type pluginWorktreeCreated struct {
	MainRepo string `json:"main_repo"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
}

type pluginWorktreeDelete struct {
	MainRepo string `json:"main_repo"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Force    bool   `json:"force"`
}

type pluginWorktreeAnswer struct {
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
	Branch string `json:"branch,omitempty"`
	Error  string `json:"error,omitempty"`
}

func pluginWorktreesListed(app *testworld.Peer, repo string) []protocol.Worktree {
	app.T.Helper()
	return testworld.Request(app, protocol.ListWorktreesMessage{Cmd: protocol.CmdListWorktrees, MainRepo: repo}, protocol.EventWorktreesUpdated,
		func(protocol.WebSocketEvent) bool { return true }).Worktrees
}

func pluginWorktreeListedOn(app *testworld.Peer, repo, path string) (string, bool) {
	app.T.Helper()
	for _, worktree := range pluginWorktreesListed(app, repo) {
		if worktree.Path == path {
			return worktree.Branch, true
		}
	}
	return "", false
}

func pluginWorktreeBranchExists(repo, branch string) bool {
	return exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

func pluginWorktreeOrigin(t *testing.T, repo string, branches ...string) {
	t.Helper()
	origin := filepath.Join(filepath.Dir(repo), "origin.git")
	runGit(t, filepath.Dir(repo), "init", "-q", "--bare", "-b", "main", origin)
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-q", "origin", "main")
	for _, branch := range branches {
		runGit(t, repo, "push", "-q", "origin", "main:"+branch)
	}
	runGit(t, repo, "fetch", "-q", "origin")
}

func TestAWorktreeProviderCreatesOrDeclinesTheWorktreesAttnRegisters(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	root := filepath.Dir(repo)
	runGit(t, repo, "branch", "feature/existing")
	pluginWorktreeOrigin(t, repo, "feature/remote-fallback")
	existing := filepath.Join(root, "existing")
	runGit(t, repo, "worktree", "add", "-q", "-b", "feat/existing", existing)
	provider := connectPlugin(t, w, "custom-create-provider", "worktree.create")

	handled := func(path string, add ...string) func(pluginWorktreeCreate) pluginWorktreeAnswer {
		return func(asked pluginWorktreeCreate) pluginWorktreeAnswer {
			runGit(t, repo, append([]string{"worktree", "add", "-q"}, add...)...)
			return pluginWorktreeAnswer{Status: "handled", Path: path, Branch: asked.Branch}
		}
	}
	declined := func(pluginWorktreeCreate) pluginWorktreeAnswer { return pluginWorktreeAnswer{Status: "decline"} }

	for _, row := range []struct {
		name                 string
		request              any
		branch, startingFrom string
		provide              func(pluginWorktreeCreate) pluginWorktreeAnswer
		path, refusal        string
	}{
		{name: "a new branch the provider creates",
			request: protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat/provider-create"},
			branch:  "feat/provider-create", path: filepath.Join(root, "provider-create"),
			provide: handled(filepath.Join(root, "provider-create"), "-b", "feat/provider-create", filepath.Join(root, "provider-create"))},
		{name: "an existing branch the provider checks out",
			request: protocol.CreateWorktreeFromBranchMessage{Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: "feature/existing"},
			branch:  "feature/existing", startingFrom: "feature/existing", path: filepath.Join(root, "provider-existing"),
			provide: handled(filepath.Join(root, "provider-existing"), filepath.Join(root, "provider-existing"), "feature/existing")},
		{name: "a remote branch the provider checks out locally",
			request: protocol.CreateWorktreeFromBranchMessage{Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: "origin/feature/remote"},
			branch:  "feature/remote", startingFrom: "origin/feature/remote", path: filepath.Join(root, "provider-remote"),
			provide: handled(filepath.Join(root, "provider-remote"), "-b", "feature/remote", filepath.Join(root, "provider-remote"))},
		{name: "a new branch the provider declines",
			request: protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat/fallback-create"},
			branch:  "feat/fallback-create", path: filepath.Join(root, "shop--feat-fallback-create"), provide: declined},
		{name: "a remote branch the provider declines",
			request: protocol.CreateWorktreeFromBranchMessage{Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: "origin/feature/remote-fallback"},
			branch:  "feature/remote-fallback", startingFrom: "origin/feature/remote-fallback", path: filepath.Join(root, "shop--feature-remote-fallback"), provide: declined},
		{name: "a path that is not a worktree",
			request: protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat/invalid-create"},
			branch:  "feat/invalid-create", refusal: "is not a worktree",
			provide: func(asked pluginWorktreeCreate) pluginWorktreeAnswer {
				return pluginWorktreeAnswer{Status: "handled", Path: filepath.Join(root, "not-a-worktree"), Branch: asked.Branch}
			}},
		{name: "a worktree that existed before the request",
			request: protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat/new-request"},
			branch:  "feat/new-request", refusal: "already existed before provider create",
			provide: func(pluginWorktreeCreate) pluginWorktreeAnswer {
				return pluginWorktreeAnswer{Status: "handled", Path: existing, Branch: "feat/existing"}
			}},
	} {
		app.Send(row.request)
		var asked pluginWorktreeCreate
		id := provider.expect("worktree.create", &asked)
		if asked.MainRepo != repo || asked.Branch != row.branch || asked.StartingFrom != row.startingFrom || asked.RequestedPath != nil {
			t.Errorf("%s: the provider was asked %+v, want %s on %s from %q with no requested path", row.name, asked, repo, row.branch, row.startingFrom)
		}
		provider.answer(id, row.provide(asked))
		result := testworld.Await(app, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
		if row.refusal != "" {
			if result.Success || !strings.Contains(protocol.Deref(result.Error), row.refusal) {
				t.Errorf("%s: create answered %+v, want a refusal saying %q", row.name, result, row.refusal)
			}
			continue
		}
		if !result.Success || protocol.Deref(result.Path) != row.path {
			t.Errorf("%s: create answered %+v (%s), want %s", row.name, result, protocol.Deref(result.Error), row.path)
			continue
		}
		if branch, listed := pluginWorktreeListedOn(app, repo, row.path); !listed || branch != row.branch {
			t.Errorf("%s: the app lists %s on %q (listed %v), want it on %s", row.name, row.path, branch, listed, row.branch)
		}
	}
}

func TestWorktreeHooksRunAroundACreateAndAFailingAfterHookKeepsTheWorktree(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	path := filepath.Join(filepath.Dir(repo), "shop--feat-hooked")
	before := connectPlugin(t, w, "before-create-hook", "worktree.before_create")
	after := connectPlugin(t, w, "after-create-hook", "worktree.after_create")

	app.Send(protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat/hooked"})
	var asked pluginWorktreeCreate
	id := before.expect("worktree.before_create", &asked)
	if asked.MainRepo != repo || asked.Branch != "feat/hooked" {
		t.Errorf("the before hook was asked %+v, want %s on feat/hooked", asked, repo)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree existed (%v) before the before hook answered", err)
	}
	before.answer(id, map[string]any{})

	var created pluginWorktreeCreated
	id = after.expect("worktree.after_create", &created)
	if created != (pluginWorktreeCreated{MainRepo: repo, Path: path, Branch: "feat/hooked"}) {
		t.Errorf("the after hook was told %+v, want %s created on feat/hooked", created, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the worktree is missing when the after hook runs: %v", err)
	}
	after.fail(id, "dependency bootstrap failed")

	result := testworld.Await(app, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
	if result.Success || protocol.Deref(result.Path) != path || !strings.Contains(protocol.Deref(result.Error), "dependency bootstrap failed") {
		t.Errorf("create answered %+v, want the created path with the after hook's error", result)
	}
	if branch, listed := pluginWorktreeListedOn(app, repo, path); !listed || branch != "feat/hooked" {
		t.Errorf("the app lists %s on %q (listed %v), want it kept on feat/hooked", path, branch, listed)
	}
}

func TestADelegationReportsTheProviderWorktreeWhenItsAfterCreateHookFails(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	providerPath := filepath.Join(filepath.Dir(repo), "provider-actual")
	source := w.Spawn(app, fakeagent.Codex, repo)
	provider := connectPlugin(t, w, "delegation-path-provider", "worktree.create")
	hook := connectPlugin(t, w, "delegation-after-hook", "worktree.after_create")

	request := delegateCheckoutAt(repo, delegateNewWorktree("feat/provider-path", "HEAD"))
	request.SourceSessionID = protocol.Ptr(source)
	request.RequestID = "provider-path"
	delegated := make(chan error, 1)
	go func() {
		_, err := w.Client().Delegate(request)
		delegated <- err
	}()

	var asked pluginWorktreeCreate
	id := provider.expect("worktree.create", &asked)
	runGit(t, repo, "worktree", "add", "-q", "-b", asked.Branch, providerPath, "HEAD")
	provider.answer(id, pluginWorktreeAnswer{Status: "handled", Path: providerPath, Branch: asked.Branch})
	hook.fail(hook.expect("worktree.after_create", nil), "dependency bootstrap failed")

	select {
	case err := <-delegated:
		if err == nil {
			t.Fatal("the delegation succeeded, want it failed by the after hook")
		}
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("the delegation did not settle within %s", fakeagent.HangGuard)
	}
	operation, err := cli.DelegationStatus("provider-path")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != protocol.DelegationOperationStateFailed || protocol.Deref(operation.WorktreePath) != providerPath {
		t.Errorf("the delegation reports %s at %q, want failed at the provider's %s", operation.State, protocol.Deref(operation.WorktreePath), providerPath)
	}
}

func TestAWorktreeProviderGetsTwoMinutesToCreateAndThirtySecondsToDelete(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		repo := newRepo(t, "shop")
		deleter := connectPlugin(t, w, "slow-delete-provider", "worktree.delete")
		doomed := createWorktree(t, app, repo, "feat-doomed")
		creator := connectPlugin(t, w, "slow-create-provider", "worktree.create")

		answered := automationEventCount(app, protocol.EventCreateWorktreeResult)
		app.Send(protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat/slow"})
		creator.expect("worktree.create", nil)
		w.advance(2*time.Minute - time.Second)
		if automationEventCount(app, protocol.EventCreateWorktreeResult) != answered {
			t.Fatal("create gave up on the provider before its two minutes ran out")
		}
		w.advance(time.Second)
		created := testworld.Await(app, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
		if created.Success || !strings.Contains(protocol.Deref(created.Error), "slow-create-provider") {
			t.Errorf("create answered %+v, want it failed on the silent provider", created)
		}

		app.Send(protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: doomed})
		deleter.expect("worktree.delete", nil)
		w.advance(30*time.Second - time.Second)
		if automationEventCount(app, protocol.EventDeleteWorktreeResult) != 0 {
			t.Fatal("delete gave up on the provider before its thirty seconds ran out")
		}
		w.advance(time.Second)
		deleted := testworld.Await(app, protocol.EventDeleteWorktreeResult, func(protocol.DeleteWorktreeResultMessage) bool { return true })
		if deleted.Success || !strings.Contains(protocol.Deref(deleted.Error), "slow-delete-provider") {
			t.Errorf("delete answered %+v, want it failed on the silent provider", deleted)
		}
		if _, listed := pluginWorktreeListedOn(app, repo, doomed); !listed {
			t.Error("the worktree the silent provider never deleted is no longer listed")
		}
	})
}

func TestAWorktreeDeleteProviderDecidesHowAWorktreeIsRemoved(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	provider := connectPlugin(t, w, "custom-delete-provider", "worktree.delete")

	removeForced := func(asked pluginWorktreeDelete) {
		runGit(t, repo, "worktree", "remove", "--force", asked.Path)
	}
	for _, row := range []struct {
		name, branch     string
		dirty, force     bool
		provide          func(pluginWorktreeDelete) pluginWorktreeAnswer
		reason, error    string
		forceable, stays bool
		branchGone       bool
		retryWithForce   bool
	}{
		{name: "handled", branch: "feat-provider-delete", force: true, branchGone: true,
			provide: func(asked pluginWorktreeDelete) pluginWorktreeAnswer {
				removeForced(asked)
				return pluginWorktreeAnswer{Status: "handled"}
			}},
		{name: "declined", branch: "feat-declined-delete", branchGone: true,
			provide: func(pluginWorktreeDelete) pluginWorktreeAnswer { return pluginWorktreeAnswer{Status: "decline"} }},
		{name: "refused as dirty", branch: "feat-provider-dirty-delete", dirty: true, stays: true,
			reason: "dirty_worktree", forceable: true, retryWithForce: true, branchGone: true,
			provide: func(pluginWorktreeDelete) pluginWorktreeAnswer {
				return pluginWorktreeAnswer{Status: "error", Error: "fatal: worktree contains modified or untracked files, use --force to delete it"}
			}},
		{name: "refused by policy", branch: "feat-provider-error-delete", force: true, stays: true,
			reason: "provider_error", error: "custom policy failed",
			provide: func(pluginWorktreeDelete) pluginWorktreeAnswer {
				return pluginWorktreeAnswer{Status: "error", Error: "custom policy failed"}
			}},
		{name: "deleted before an error", branch: "feat-provider-delete-before-error", force: true, branchGone: true,
			provide: func(asked pluginWorktreeDelete) pluginWorktreeAnswer {
				removeForced(asked)
				return pluginWorktreeAnswer{Status: "error", Error: "connection lost after delete"}
			}},
	} {
		path := createWorktree(t, app, repo, row.branch)
		if row.dirty {
			if err := os.WriteFile(filepath.Join(path, "local.txt"), []byte("local change\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		deleteThroughProvider := func(force bool) protocol.DeleteWorktreeResultMessage {
			t.Helper()
			app.Send(protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: path, Force: protocol.Ptr(force)})
			var asked pluginWorktreeDelete
			id := provider.expect("worktree.delete", &asked)
			if asked != (pluginWorktreeDelete{MainRepo: repo, Path: path, Branch: row.branch, Force: force}) {
				t.Errorf("%s: the provider was asked %+v, want %s on %s with force %v", row.name, asked, path, row.branch, force)
			}
			if force && row.retryWithForce {
				runGit(t, repo, "worktree", "remove", "--force", asked.Path)
				provider.answer(id, pluginWorktreeAnswer{Status: "handled"})
			} else {
				provider.answer(id, row.provide(asked))
			}
			return testworld.Await(app, protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == path })
		}

		result := deleteThroughProvider(row.force)
		if row.stays {
			if result.Success || result.ReasonKind != row.reason || protocol.Deref(result.Forceable) != row.forceable ||
				!strings.Contains(protocol.Deref(result.Error), row.error) {
				t.Errorf("%s: delete answered %+v (%s), want a %s refusal, forceable %v", row.name, result, protocol.Deref(result.Error), row.reason, row.forceable)
			}
			if _, listed := pluginWorktreeListedOn(app, repo, path); !listed {
				t.Errorf("%s: the refused worktree is no longer listed", row.name)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s: the refused worktree is gone from disk: %v", row.name, err)
			}
			if !row.retryWithForce {
				continue
			}
			result = deleteThroughProvider(true)
		}
		if !result.Success {
			t.Errorf("%s: delete answered %+v (%s), want success", row.name, result, protocol.Deref(result.Error))
			continue
		}
		if _, listed := pluginWorktreeListedOn(app, repo, path); listed {
			t.Errorf("%s: the deleted worktree is still listed", row.name)
		}
		if row.branchGone && pluginWorktreeBranchExists(repo, row.branch) {
			t.Errorf("%s: the branch %s outlived its worktree", row.name, row.branch)
		}
		removals, err := cli.WorktreeSweepLog(repo, 100)
		if err != nil {
			t.Fatal(err)
		}
		if count := len(slices.DeleteFunc(sweptPaths(removals.Entries), func(p string) bool { return p != path })); count != 1 {
			t.Errorf("%s: the worktree log records %d removals of %s, want one", row.name, count, path)
		}
	}
}

func TestTheSweepRemovesAWorktreeThroughItsDeleteProviderAndItsBranchWithIt(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	pluginWorktreeOrigin(t, repo)
	provider := connectPlugin(t, w, "sweep-delete-provider", "worktree.delete")

	removals := map[string]func(path string) pluginWorktreeAnswer{
		"feat-removed-by-git": func(path string) pluginWorktreeAnswer {
			runGit(t, repo, "worktree", "remove", path)
			return pluginWorktreeAnswer{Status: "handled"}
		},
		"feat-removed-from-disk": func(path string) pluginWorktreeAnswer {
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			return pluginWorktreeAnswer{Status: "handled"}
		},
		"feat-removed-then-errored": func(path string) pluginWorktreeAnswer {
			runGit(t, repo, "worktree", "remove", path)
			return pluginWorktreeAnswer{Status: "error", Error: "provider lost its response after deletion"}
		},
	}
	paths := map[string]string{}
	for branch := range removals {
		paths[createWorktree(t, app, repo, branch)] = branch
	}

	refreshWorktrees(t, cli)
	for range removals {
		var asked pluginWorktreeDelete
		id := provider.expect("worktree.delete", &asked)
		branch, ours := paths[asked.Path]
		if !ours || asked.Branch != branch {
			t.Fatalf("the sweep asked the provider to delete %+v, want one of %v", asked, paths)
		}
		provider.answer(id, removals[branch](asked.Path))
	}
	for path, branch := range paths {
		swept := testworld.Await(app, protocol.EventWorktreeSwept, func(e protocol.WebSocketEvent) bool {
			return e.SweepEntry != nil && e.SweepEntry.Path == path
		}).SweepEntry
		if swept.Action != "removed" {
			t.Errorf("the sweep recorded %s for %s, want it removed", swept.Action, branch)
		}
		if pluginWorktreeBranchExists(repo, branch) {
			t.Errorf("the sweep left the branch %s behind", branch)
		}
		if _, listed := pluginWorktreeListedOn(app, repo, path); listed {
			t.Errorf("the swept worktree %s is still listed", branch)
		}
	}
}

func TestTheSweepKeepsAProviderDeletedWorktreeGitCouldNotForgetSoADeleteCanFinishIt(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	pluginWorktreeOrigin(t, repo)
	provider := connectPlugin(t, w, "unprunable-delete-provider", "worktree.delete")
	path := createWorktree(t, app, repo, "feat-unprunable")
	config := filepath.Join(repo, ".git", "config")
	healthyConfig, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}

	refreshWorktrees(t, cli)
	id := provider.expect("worktree.delete", nil)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, append(slices.Clone(healthyConfig), "[unterminated\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	provider.answer(id, pluginWorktreeAnswer{Status: "handled"})
	swept := testworld.Await(app, protocol.EventWorktreeSwept, func(e protocol.WebSocketEvent) bool {
		return e.SweepEntry != nil && e.SweepEntry.Path == path
	}).SweepEntry
	if swept.Action != "failed" {
		t.Errorf("the sweep recorded %s (%s), want it failed while Git cannot prune the registration", swept.Action, protocol.Deref(swept.Reason))
	}
	if _, listed := pluginWorktreeListedOn(app, repo, path); !listed {
		t.Fatal("the daemon dropped a worktree whose Git registration was never pruned, so nothing can finish its removal")
	}

	if err := os.WriteFile(config, healthyConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	app.Send(protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: path})
	provider.answer(provider.expect("worktree.delete", nil), pluginWorktreeAnswer{Status: "handled"})
	deleted := testworld.Await(app, protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == path })
	if !deleted.Success {
		t.Fatalf("deleting the kept worktree answered %+v (%s), want success", deleted, protocol.Deref(deleted.Error))
	}
	if out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output(); err != nil || strings.Contains(string(out), path) {
		t.Errorf("git still registers %s after the delete (%v): %s", path, err, out)
	}
	if pluginWorktreeBranchExists(repo, "feat-unprunable") {
		t.Error("the delete left the branch feat-unprunable behind")
	}
}

func TestAnInstalledWorktreeProviderRunsAndComesBackAfterItExits(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t)
	repo := newRepo(t, "shop")
	writePluginManifest(t, filepath.Join(w.Dir, "plugins", "installed-provider"), "installed-provider",
		"ATTN_PLUGIN_HELPER=1 exec '"+self+"' -test.run '^TestPluginWorktreeProviderProcess$'")
	w.restart()
	app := w.App()

	restarted := func(p protocol.PluginInfo) bool {
		return p.Connected && protocol.Deref(p.RuntimePhase) == "connected" && protocol.Deref(p.RestartAttempt) == 1
	}
	if shown, _ := pluginNamed(listPlugins(app).Plugins, "installed-provider"); !restarted(shown) {
		awaitPluginShown(app, "installed-provider", restarted)
	}

	created := testworld.Request(app, protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat-installed"},
		protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
	path := protocol.Deref(created.Path)
	if want := filepath.Join(filepath.Dir(repo), "provided-by-generation-2"); !created.Success || path != want {
		t.Fatalf("create answered %+v (%s), want the restarted provider's %s", created, protocol.Deref(created.Error), want)
	}
	if branch, listed := pluginWorktreeListedOn(app, repo, path); !listed || branch != "feat-installed" {
		t.Errorf("the app lists %s on %q (listed %v), want it on feat-installed", path, branch, listed)
	}
}

func TestPluginWorktreeProviderProcess(t *testing.T) {
	if os.Getenv("ATTN_PLUGIN_HELPER") != "1" {
		return
	}
	generation, err := strconv.ParseUint(os.Getenv("ATTN_PLUGIN_GENERATION"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", os.Getenv("ATTN_SOCKET_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello",
		"params": pluginHelloParams(os.Getenv("ATTN_PLUGIN_NAME"), pluginWireAPIVersion, generation, "worktree.create")}); err != nil {
		t.Fatal(err)
	}
	var hello pluginWireMessage
	if err := decoder.Decode(&hello); err != nil || hello.Error != nil {
		t.Fatalf("hello answered %+v: %v", hello, err)
	}
	if generation == 1 {
		return
	}
	for {
		var request pluginWireMessage
		if err := decoder.Decode(&request); err != nil {
			return
		}
		var result any = pluginHealth{OK: true}
		if request.Method == "worktree.create" {
			var asked pluginWorktreeCreate
			if err := json.Unmarshal(request.Params, &asked); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(filepath.Dir(asked.MainRepo), "provided-by-generation-"+strconv.FormatUint(generation, 10))
			if out, err := exec.Command("git", "-C", asked.MainRepo, "worktree", "add", "-q", "-b", asked.Branch, path).CombinedOutput(); err != nil {
				t.Fatalf("git worktree add: %v: %s", err, out)
			}
			result = pluginWorktreeAnswer{Status: "handled", Path: path, Branch: asked.Branch}
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			return
		}
	}
}
