package daemon_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func delegateCheckoutAt(cwd string, checkout *protocol.DelegateCheckout) protocol.DelegateMessage {
	request := brief(cwd, "Add a discount field")
	request.Agent = protocol.Ptr("codex")
	request.Checkout = checkout
	return request
}

func delegateNewWorktree(branch, from string) *protocol.DelegateCheckout {
	return &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: branch, From: protocol.Ptr(from)}
}

func TestADelegatedWorktreeStartsFromTheExactBaseWhereItWasAsked(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	root := filepath.Dir(repo)
	base := commitFile(t, repo, "web.txt", "shop\n")
	runGit(t, repo, "branch", "base")
	if err := os.MkdirAll(filepath.Join(repo, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := commitFile(t, filepath.Join(repo, "web"), "index.html", "shop\n")
	origin := filepath.Join(root, "origin.git")
	runGit(t, root, "init", "--bare", "-b", "main", origin)
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-q", "origin", "main")
	runGit(t, repo, "fetch", "-q", "origin")
	upstream := filepath.Join(root, "upstream")
	runGit(t, root, "clone", "-q", origin, upstream)
	commitFile(t, upstream, "later.txt", "later\n")
	runGit(t, upstream, "push", "-q", "origin", "main")
	source := w.Spawn(app, fakeagent.Codex, repo)
	outsideGit := w.Path("notes")
	if err := os.MkdirAll(outsideGit, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		name, source, cwd   string
		checkout            *protocol.DelegateCheckout
		wantDirectory, head string
		wantWorkspace       string
		wantLabel           string
	}{
		{name: "from the caller's checkout", source: source, cwd: repo, checkout: delegateNewWorktree("feature/a", "base"),
			wantDirectory: filepath.Join(root, "shop--feature-a"), head: base, wantWorkspace: "workspace-shop"},
		{name: "from a subdirectory, on a stale remote ref", cwd: filepath.Join(repo, "web"), checkout: delegateNewWorktree("feature/b", "origin/main"),
			wantDirectory: filepath.Join(root, "shop--feature-b", "web"), head: local, wantLabel: "web"},
		{name: "on a branch name longer than a session name", cwd: repo, checkout: delegateNewWorktree("feat/delegated-with-a-branch-name-past-the-cap", "main"),
			wantDirectory: filepath.Join(root, "shop--feat-delegated-with-a-branch-name-past-the-cap"), head: local, wantLabel: "shop--feat-delegated-with-a-branch-name-past-the"},
		{name: "outside Git", cwd: outsideGit, wantDirectory: outsideGit},
	} {
		request := delegateCheckoutAt(row.cwd, row.checkout)
		if row.source != "" {
			request.SourceSessionID = protocol.Ptr(row.source)
			request.Label = protocol.Ptr("discount")
		}
		result, err := cli.Delegate(request)
		if err != nil {
			t.Errorf("%s: %v", row.name, err)
			continue
		}
		if result.Directory != row.wantDirectory {
			t.Errorf("%s: the delegate works in %s; want %s", row.name, result.Directory, row.wantDirectory)
		}
		if row.checkout == nil {
			if result.Checkout != "reused" || result.WorktreeCreated != nil {
				t.Errorf("%s: the delegation reports checkout %q, created %v; want no worktree", row.name, result.Checkout, result.WorktreeCreated)
			}
			continue
		}
		if result.Checkout != "created" || !protocol.Deref(result.WorktreeCreated) || protocol.Deref(result.Branch) != row.checkout.Branch {
			t.Errorf("%s: the delegation reports checkout %q, created %v, branch %q; want a created worktree on %s",
				row.name, result.Checkout, result.WorktreeCreated, protocol.Deref(result.Branch), row.checkout.Branch)
		}
		if head := strings.TrimSpace(runGit(t, result.Directory, "rev-parse", "HEAD")); head != row.head {
			t.Errorf("%s: the worktree starts at %s; want %s from %s", row.name, head, row.head, protocol.Deref(row.checkout.From))
		}
		if row.wantWorkspace != "" && protocol.Deref(result.WorkspaceID) != row.wantWorkspace {
			t.Errorf("%s: the delegate landed in workspace %s; want the caller's %s", row.name, protocol.Deref(result.WorkspaceID), row.wantWorkspace)
		}
		if row.wantLabel != "" {
			if label := sessionOfDelegate(t, w, result.SessionID).Label; label != row.wantLabel {
				t.Errorf("%s: the delegate is named %q; want %q", row.name, label, row.wantLabel)
			}
		}
	}
	if fetched := strings.TrimSpace(runGit(t, repo, "rev-parse", "origin/main")); fetched != local {
		t.Errorf("the repository's origin/main moved to %s; a delegation must not fetch", fetched)
	}
}

func TestADelegatedCheckoutThatCannotBeHonouredIsRefusedByName(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	repo := newRepo(t, "shop")
	root := filepath.Dir(repo)
	origin := filepath.Join(root, "origin.git")
	runGit(t, root, "init", "--bare", "-b", "main", origin)
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-q", "origin", "main:remote-only")
	runGit(t, repo, "fetch", "-q", "origin")
	detached := filepath.Join(root, "shop--detached")
	runGit(t, repo, "worktree", "add", "-q", "--detach", detached)
	otherRepo := newRepo(t, "billing")
	outsideGit := w.Path("notes")
	if err := os.MkdirAll(outsideGit, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreesBefore := runGit(t, repo, "worktree", "list")

	for _, row := range []struct {
		name, cwd string
		checkout  *protocol.DelegateCheckout
		refusal   string
	}{
		{name: "checkout flags outside Git", cwd: outsideGit, checkout: delegateNewWorktree("feature/a", "main"),
			refusal: "checkout flags are invalid outside Git: " + outsideGit},
		{name: "a repository without a checkout choice", cwd: repo,
			refusal: "git checkout requires reuse or new_worktree"},
		{name: "a base that is not there", cwd: repo, checkout: delegateNewWorktree("feature/a", "nope"),
			refusal: `base ref "nope" is unavailable`},
		{name: "an existing branch only the remote has", cwd: repo,
			checkout: &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "remote-only"},
			refusal:  `local branch "remote-only" does not exist`},
		{name: "reusing a detached checkout", cwd: detached,
			checkout: &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main"},
			refusal:  "cannot reuse detached"},
		{name: "reusing a checkout on another branch", cwd: repo,
			checkout: &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "other"},
			refusal:  "branch mismatch: expected other"},
		{name: "a worktree path inside another repository", cwd: repo,
			checkout: &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/a", From: protocol.Ptr("main"), Path: protocol.Ptr(otherRepo)},
			refusal:  "--worktree-path resolves to " + otherRepo},
	} {
		if result, err := cli.Delegate(delegateCheckoutAt(row.cwd, row.checkout)); err == nil || !strings.Contains(err.Error(), row.refusal) {
			t.Errorf("%s: delegating = %+v, %v; want the refusal %q", row.name, result, err, row.refusal)
		}
	}

	if after := runGit(t, repo, "worktree", "list"); after != worktreesBefore {
		t.Errorf("refused delegations changed the worktrees:\n%s\nwas\n%s", after, worktreesBefore)
	}
	if branches := runGit(t, repo, "branch", "--list", "feature/*"); strings.TrimSpace(branches) != "" {
		t.Errorf("refused delegations left branches behind: %s", branches)
	}
	if sessions, err := cli.Query(""); err != nil || len(sessions) != 0 {
		t.Errorf("refused delegations left sessions %+v, %v", sessions, err)
	}
}

func TestADelegationNeverSharesACheckoutWithoutConsent(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	repo := newRepo(t, "shop")
	shared := filepath.Join(filepath.Dir(repo), "shop--feature-shared")
	nested := filepath.Join(shared, "nested", "package")

	owner, err := cli.Delegate(delegateCheckoutAt(repo, delegateNewWorktree("feature/shared", "main")))
	if err != nil {
		t.Fatalf("creating the shared worktree: %v", err)
	}
	if owner.Directory != shared {
		t.Fatalf("the first delegate works in %s; want %s", owner.Directory, shared)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	reuse := &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "feature/shared"}
	recreate := &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "feature/shared", Path: protocol.Ptr(shared)}

	for i, row := range []struct {
		name     string
		request  protocol.DelegateMessage
		consent  bool
		refusal  string
		sharedIn string
	}{
		{name: "creating the occupied worktree again", request: delegateCheckoutAt(repo, recreate),
			refusal: "cannot be reinterpreted as checkout reuse"},
		{name: "creating the occupied worktree again, consenting to share", request: delegateCheckoutAt(repo, recreate), consent: true,
			refusal: "cannot be reinterpreted as checkout reuse"},
		{name: "reusing the occupied worktree", request: delegateCheckoutAt(shared, reuse),
			refusal: "checkout " + shared + " is used by active Attn session " + owner.SessionID + "; pass --allow-worktree-reuse"},
		{name: "reusing a folder inside the occupied worktree", request: delegateCheckoutAt(nested, reuse),
			refusal: "checkout " + shared + " is used by active Attn session"},
		{name: "reusing the occupied worktree, consenting to share", request: delegateCheckoutAt(shared, reuse), consent: true, sharedIn: shared},
		{name: "reusing a folder inside it, consenting to share", request: delegateCheckoutAt(nested, reuse), consent: true, sharedIn: nested},
	} {
		row.request.Label = protocol.Ptr("sharer-" + strconv.Itoa(i))
		if row.consent {
			row.request.AllowWorktreeReuse = protocol.Ptr(true)
		}
		result, err := cli.Delegate(row.request)
		if row.refusal != "" {
			if err == nil || !strings.Contains(err.Error(), row.refusal) {
				t.Errorf("%s: delegating = %+v, %v; want the refusal %q", row.name, result, err, row.refusal)
			}
			continue
		}
		if err != nil || result.Directory != row.sharedIn || result.Checkout != "reused" {
			t.Errorf("%s: delegating = %+v, %v; want to share the checkout at %s", row.name, result, err, row.sharedIn)
		}
	}

	w.restart()
	afterRestart := delegateCheckoutAt(shared, reuse)
	afterRestart.Label = protocol.Ptr("after-restart")
	if result, err := w.Client().Delegate(afterRestart); err != nil || result.Directory != shared {
		t.Fatalf("reusing the worktree once its sessions only survive as recoverable = %+v, %v; want it free", result, err)
	}
}

func TestADelegateStillBootingHoldsItsCheckoutAgainstAnother(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	reuseMain := &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main"}
	first := delegateCheckoutAt(repo, reuseMain)
	first.RequestID, first.Label = "first", protocol.Ptr("first")
	boot := w.HoldNextBoot()
	accepted, err := cli.StartDelegation(first)
	if err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(m protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range m.WorkspaceLayout.Panes {
			if protocol.Deref(pane.SessionID) == accepted.SessionID && pane.Status == protocol.WorkspaceLayoutPaneStatusReady {
				return true
			}
		}
		return false
	})

	second := delegateCheckoutAt(repo, reuseMain)
	second.Label = protocol.Ptr("second")
	if result, err := cli.Delegate(second); err == nil || !strings.Contains(err.Error(), "checkout "+repo+" is used by active Attn session "+accepted.SessionID) {
		t.Errorf("a second delegation into the checkout the first is still launching in = %+v, %v; want it refused", result, err)
	}
	boot()
	if result, err := cli.Delegate(first); err != nil || result.SessionID != accepted.SessionID || result.Directory != repo {
		t.Fatalf("the first delegation = %+v, %v; want it to finish in %s", result, err, repo)
	}
}
