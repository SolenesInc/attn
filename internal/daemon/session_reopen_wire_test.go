package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestReopeningALiveOrUnknownSessionChangesNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	live := w.Spawn(app, fakeagent.Codex, w.Path("api"))
	w.Launched(live)

	shown, err := cli.SessionShow(live)
	if err != nil {
		t.Fatalf("session show %s: %v", live, err)
	}
	if verdict := shown.Reopen; verdict == nil || verdict.Reopenable || len(verdict.Actions) != 0 || !strings.Contains(protocol.Deref(verdict.Reason), "focus it") {
		t.Errorf("the verdict on a live session = %+v, want no reopen and a reason sending the caller to focus it", verdict)
	}
	reopened := reopenOverTheWebSocket(app, live)
	if !reopened.Success || reopened.Result == nil || !protocol.Deref(reopened.Result.AlreadyRunning) {
		t.Errorf("reopening a live session = %+v, want it reported as already running", reopened)
	}

	_, err = cli.SessionReopen(client.SessionReopenOptions{SessionID: "never-ran"})
	for _, want := range []string{"never-ran", "ledger", "seed"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("reopening a session that never ran = %v, want an error naming %q", err, want)
		}
	}
}

func TestAClosedSessionWhoseConversationIsGoneOffersOnlyAFreshStart(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()

	boot := w.HoldNextBoot()
	unbound := w.Spawn(app, fakeagent.Codex, w.Path("unbound"))
	closeSession(t, cli, unbound, "never started")
	awaitClosed(app, unbound)
	boot()

	vanished := w.Spawn(app, fakeagent.Codex, w.Path("vanished"))
	conversation := w.Launched(vanished).ConversationID
	closeSession(t, cli, vanished, "done")
	awaitClosed(app, vanished)
	removeReopenRollout(t, w, conversation)

	for session, reason := range map[string]string{
		unbound:  "nothing to resume",
		vanished: "no longer in codex's storage",
	} {
		verdict := reopenVerdict(t, cli, session)
		if verdict.Reopenable || !slices.Equal(verdict.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace}) {
			t.Errorf("the verdict on %s = %+v, want only a fresh start in place", session, verdict)
		}
		if !strings.Contains(protocol.Deref(verdict.Reason), reason) {
			t.Errorf("the reason %s cannot resume is %q, want it to mention %q", session, protocol.Deref(verdict.Reason), reason)
		}
	}
}

func TestTheReopenVerdictReadsWhatBecameOfTheDirectory(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := reopenRepoWithOrigin(t)
	present := reopenWorktree(t, repo, "feat/present")
	switched := reopenWorktree(t, repo, "feat/switched")
	lostRepo := reopenRepoWithOrigin(t)
	orphaned := reopenWorktree(t, lostRepo, "feat/orphaned")
	plain := w.Path("plain")
	unreadable := w.Path("unreadable")

	sessions := map[string]string{}
	for name, dir := range map[string]string{"present": present, "switched": switched, "orphaned": orphaned, "plain": plain, "unreadable": unreadable} {
		sessions[name] = closedReopenCodex(t, w, app, cli, dir)
	}
	runGit(t, switched, "switch", "-c", "feat/somewhere-else")
	for _, gone := range []string{plain, unreadable, orphaned, filepath.Dir(lostRepo)} {
		if err := os.RemoveAll(gone); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(unreadable, []byte("this is a file"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]struct {
		reopenable bool
		actions    []protocol.SessionReopenAction
		reason     string
		warning    string
	}{
		"present":    {true, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}, "", ""},
		"switched":   {true, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}, "", "feat/somewhere-else"},
		"unreadable": {false, nil, "cannot be opened", ""},
		"plain":      {false, []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}, "not a worktree attn can put back", ""},
		"orphaned":   {false, nil, "repository is gone", ""},
	} {
		verdict := reopenVerdict(t, cli, sessions[name])
		if verdict.Reopenable != want.reopenable || !slices.Equal(verdict.Actions, want.actions) {
			t.Errorf("%s: reopenable %v offering %v (%q), want %v offering %v", name, verdict.Reopenable, verdict.Actions, protocol.Deref(verdict.Reason), want.reopenable, want.actions)
		}
		if !strings.Contains(protocol.Deref(verdict.Reason), want.reason) {
			t.Errorf("%s: reason %q, want it to mention %q", name, protocol.Deref(verdict.Reason), want.reason)
		}
		if warning := protocol.Deref(verdict.Warning); (want.warning == "") != (warning == "") || !strings.Contains(warning, want.warning) {
			t.Errorf("%s: warning %q, want %q", name, warning, want.warning)
		}
	}
}

func TestTheReopenVerdictFollowsWhatBecameOfTheBranch(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := reopenRepoWithOrigin(t)
	sessions := map[string]string{}
	for _, name := range []string{"local", "remote", "gone"} {
		worktree := reopenWorktree(t, repo, "feat/"+name)
		if name == "remote" {
			runGit(t, worktree, "push", "-q", "-u", "origin", "feat/remote")
		}
		sessions[name] = closedReopenCodex(t, w, app, cli, worktree)
		if err := os.RemoveAll(worktree); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "worktree", "prune")
	runGit(t, repo, "branch", "-q", "-D", "feat/remote", "feat/gone")

	for name, want := range map[string]struct {
		state   string
		actions []protocol.SessionReopenAction
		reason  string
	}{
		"local":  {"local", []protocol.SessionReopenAction{protocol.SessionReopenActionRecreateWorktreeAndReopen}, ""},
		"remote": {"remote_only", []protocol.SessionReopenAction{protocol.SessionReopenActionFetchRecreateAndReopen}, "origin"},
		"gone":   {"gone", []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshDefaultBranch, protocol.SessionReopenActionStartFreshElsewhere}, ""},
	} {
		verdict := reopenVerdict(t, cli, sessions[name])
		if protocol.Deref(verdict.BranchState) != want.state || !slices.Equal(verdict.Actions, want.actions) || !strings.Contains(protocol.Deref(verdict.Reason), want.reason) {
			t.Errorf("%s: branch %q offering %v (%q), want %q offering %v and naming %q", name,
				protocol.Deref(verdict.BranchState), verdict.Actions, protocol.Deref(verdict.Reason), want.state, want.actions, want.reason)
		}
	}

	runGit(t, repo, "branch", "-q", "feat/gone", "main")
	if again := reopenVerdict(t, cli, sessions["gone"]); protocol.Deref(again.BranchState) != "local" {
		t.Errorf("after the branch came back the verdict reads it %q, want local", protocol.Deref(again.BranchState))
	}
}

func TestEachReopenActionPutsTheWorkBackAsOffered(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := reopenRepoWithOrigin(t)
	type closed struct {
		session, dir, conversation string
	}
	closedIn := func(dir string) closed {
		session := w.Spawn(app, fakeagent.Codex, dir)
		conversation := w.Launched(session).ConversationID
		closeSession(t, cli, session, "done for now")
		awaitClosed(app, session)
		return closed{session, dir, conversation}
	}
	fetched := closedIn(reopenWorktree(t, repo, "feat/fetched"))
	runGit(t, fetched.dir, "push", "-q", "-u", "origin", "feat/fetched")
	defaulted := closedIn(reopenWorktree(t, repo, "feat/defaulted"))
	for _, dir := range []string{fetched.dir, defaulted.dir} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "worktree", "prune")
	runGit(t, repo, "branch", "-q", "-D", "feat/fetched", "feat/defaulted")
	samePlace := closedIn(w.Path("same-place"))
	removeReopenRollout(t, w, samePlace.conversation)
	elsewhere := closedIn(w.Path("elsewhere"))
	if err := os.RemoveAll(elsewhere.dir); err != nil {
		t.Fatal(err)
	}

	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: elsewhere.session, Action: string(protocol.SessionReopenActionStartFreshElsewhere)}); err == nil {
		t.Error("starting fresh elsewhere without a directory was accepted")
	}
	if closedAt := protocol.Deref(showSession(t, cli, elsewhere.session).ClosedAt); closedAt == "" {
		t.Error("the refused fresh start reopened the session")
	}

	chosen := w.Path("chosen")
	if err := os.MkdirAll(chosen, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		closed    closed
		action    protocol.SessionReopenAction
		directory string
		resumed   bool
		branch    string
	}{
		{"fetched from the remote", fetched, protocol.SessionReopenActionFetchRecreateAndReopen, fetched.dir, true, "feat/fetched"},
		{"fresh on the default branch", defaulted, protocol.SessionReopenActionStartFreshDefaultBranch, defaulted.dir, false, "feat/defaulted"},
		{"fresh in the same place", samePlace, protocol.SessionReopenActionStartFreshSamePlace, samePlace.dir, false, ""},
		{"fresh elsewhere", elsewhere, protocol.SessionReopenActionStartFreshElsewhere, chosen, false, ""},
	} {
		opts := client.SessionReopenOptions{SessionID: c.closed.session, Action: string(c.action)}
		if c.action == protocol.SessionReopenActionStartFreshElsewhere {
			opts.Directory = chosen
		}
		reopened, err := cli.SessionReopen(opts)
		if err != nil {
			t.Errorf("%s: reopen: %v", c.name, err)
			continue
		}
		run := w.Launched(c.closed.session)
		if reopened.Directory != c.directory || run.Resumed != c.resumed || (c.resumed && run.ConversationID != c.closed.conversation) {
			t.Errorf("%s: reopened in %q running codex %q, want %q resumed=%v", c.name, reopened.Directory, run.Argv, c.directory, c.resumed)
		}
		if c.branch != "" {
			if branch := strings.TrimSpace(runGit(t, c.directory, "branch", "--show-current")); branch != c.branch {
				t.Errorf("%s: the recreated worktree is on %q, want %q", c.name, branch, c.branch)
			}
		}
	}
}

func TestAReopenLandsInItsOwnWorkspaceOrOneNamedAfterIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	shared := w.Path("shared")
	kept, keptWorkspace, _ := w.RequestSpawn(app, fakeagent.Codex, shared)
	w.Launched(kept.ID)
	w.Launched(w.Spawn(app, fakeagent.Codex, shared))
	closeSession(t, cli, kept.ID, "done for now")
	awaitClosed(app, kept.ID)

	lone, loneWorkspace, lonePane := w.RequestSpawn(app, fakeagent.Codex, w.Path("alone"))
	w.Launched(lone.ID)
	closePane(app, sessionPane{session: lone.ID, workspace: loneWorkspace, pane: lonePane})
	awaitClosed(app, lone.ID)

	for _, c := range []struct {
		session, workspace, workspacePlan, panePlan string
	}{
		{kept.ID, keptWorkspace, "reuse", "add"},
		{lone.ID, "workspace-" + lone.ID, "create", "add"},
	} {
		verdict := reopenVerdict(t, cli, c.session)
		if verdict.WorkspaceID != c.workspace || verdict.WorkspacePlan != c.workspacePlan || verdict.PanePlan != c.panePlan {
			t.Errorf("%s would reopen in %s (%s) with pane plan %s, want %s (%s) with %s",
				c.session, verdict.WorkspaceID, verdict.WorkspacePlan, verdict.PanePlan, c.workspace, c.workspacePlan, c.panePlan)
		}
		reopened := reopenOverTheWebSocket(app, c.session)
		if !reopened.Success || reopened.Result == nil || reopened.Result.WorkspaceID != c.workspace {
			t.Errorf("reopening %s = %+v, want it back in %s", c.session, reopened, c.workspace)
		}
		w.Launched(c.session)
	}
	workspaces := w.App().Initial.Workspaces
	for _, c := range []struct{ session, workspace string }{{kept.ID, keptWorkspace}, {lone.ID, "workspace-" + lone.ID}} {
		if !slices.ContainsFunc(workspaces, func(ws protocol.Workspace) bool {
			return ws.ID == c.workspace && ws.Layout != nil && slices.ContainsFunc(ws.Layout.Panes, func(p protocol.WorkspaceLayoutPane) bool {
				return protocol.Deref(p.SessionID) == c.session
			})
		}) {
			t.Errorf("no pane in %s holds the reopened %s", c.workspace, c.session)
		}
	}
}

func TestEveryReopenReplaysTheSessionsLaunchContract(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	installed := filepath.Join(w.Dir, "installed")
	if err := os.Symlink(filepath.Join(w.Dir, "bin"), installed); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(installed, "codex")
	session := w.Spawn(app, fakeagent.Codex, w.Path("api"), func(m *protocol.SpawnSessionMessage) {
		m.Executable = protocol.Ptr(executable)
		m.Model = protocol.Ptr("gpt-ledger")
		m.Effort = protocol.Ptr("high")
	})
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("api")))
	conversation := w.Launched(session).ConversationID

	for _, closing := range []string{"first close", "second close"} {
		closeSession(t, cli, session, closing)
		awaitClosed(app, session)
		if reopened := reopenOverTheWebSocket(app, session); !reopened.Success {
			t.Fatalf("reopen after the %s: %s", closing, protocol.Deref(reopened.Error))
		}
		run := w.Launched(session)
		model, _ := flagValue(run.Argv, "--model")
		if !run.Resumed || run.ConversationID != conversation || run.Argv[0] != executable || model != "gpt-ledger" ||
			!slices.Contains(run.Argv, `model_reasoning_effort="high"`) {
			t.Errorf("the reopen after the %s ran %q, want %s resuming %s on gpt-ledger at high effort", closing, run.Argv, executable, conversation)
		}
	}
}

func TestTheSessionListJudgesClosedRowsOnlyWhenAskedAndAgreesWithShow(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := reopenRepoWithOrigin(t)
	var judged []string
	for _, branch := range []string{"feat/one", "feat/two"} {
		worktree := reopenWorktree(t, repo, branch)
		judged = append(judged, closedReopenCodex(t, w, app, cli, worktree))
		if err := os.RemoveAll(worktree); err != nil {
			t.Fatal(err)
		}
	}
	lapsed := newRepo(t, "lapsed")
	lapsedWorktree := reopenWorktree(t, lapsed, "feat/lapsed")
	notGit := closedReopenCodex(t, w, app, cli, lapsedWorktree)
	for _, gone := range []string{lapsedWorktree, filepath.Join(lapsed, ".git")} {
		if err := os.RemoveAll(gone); err != nil {
			t.Fatal(err)
		}
	}
	live := w.Spawn(app, fakeagent.Codex, w.Path("live"))
	w.Launched(live)

	if unasked := ledger(t, cli, client.SessionListOptions{All: true}); len(unasked.Reopen) != 0 {
		t.Errorf("a page nobody asked to judge carries verdicts %+v", unasked.Reopen)
	}
	page := ledger(t, cli, client.SessionListOptions{All: true, Reopen: true})
	if ids := ledgerIDs(page); len(ids) != 4 || !slices.Contains(ids, notGit) || !slices.Contains(ids, live) {
		t.Errorf("the page lists %v, want the live session and all three closed ones", ids)
	}
	var inRowOrder []string
	for _, entry := range page.Entries {
		if slices.Contains(judged, entry.ID) {
			inRowOrder = append(inRowOrder, entry.ID)
		}
	}
	var verdictOrder []string
	for _, entry := range page.Reopen {
		verdictOrder = append(verdictOrder, entry.SessionID)
		shown := reopenVerdict(t, cli, entry.SessionID)
		listed := entry.Reopen
		if len(listed.Actions) == 0 || protocol.Deref(listed.BranchState) != "local" {
			t.Errorf("%s is listed offering %v on a %q branch, want an action on its local branch", entry.SessionID, listed.Actions, protocol.Deref(listed.BranchState))
		}
		if listed.Reopenable != shown.Reopenable || protocol.Deref(listed.Reason) != protocol.Deref(shown.Reason) ||
			protocol.Deref(listed.BranchState) != protocol.Deref(shown.BranchState) || listed.DirectoryState != shown.DirectoryState ||
			!slices.Equal(listed.Actions, shown.Actions) {
			t.Errorf("%s is listed with %+v but shown with %+v", entry.SessionID, listed, shown)
		}
	}
	if !slices.Equal(verdictOrder, inRowOrder) {
		t.Errorf("the page judges %v, want exactly the closed git rows in row order %v", verdictOrder, inRowOrder)
	}
	shown, err := cli.SessionShow(notGit)
	if err != nil || shown.Entry.ID != notGit || shown.Reopen != nil {
		t.Errorf("showing the row whose repository is no longer git = %+v (%v), want the row without a verdict", shown, err)
	}
}

func TestReopenTellsAGitFailureFromARefusal(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := reopenRepoWithOrigin(t)
	present := closedReopenCodex(t, w, app, cli, reopenWorktree(t, repo, "feat/present"))
	missingWorktree := reopenWorktree(t, repo, "feat/missing")
	missing := closedReopenCodex(t, w, app, cli, missingWorktree)
	lostRepo := reopenRepoWithOrigin(t)
	lostWorktree := reopenWorktree(t, lostRepo, "feat/lost")
	lost := closedReopenCodex(t, w, app, cli, lostWorktree)
	for _, gone := range []string{missingWorktree, lostWorktree, filepath.Dir(lostRepo)} {
		if err := os.RemoveAll(gone); err != nil {
			t.Fatal(err)
		}
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "git"), []byte("#!/bin/sh\necho 'fatal: git is broken' >&2\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", broken+string(os.PathListSeparator)+os.Getenv("PATH"))

	if verdict := reopenVerdict(t, cli, present); !verdict.Reopenable || !slices.Equal(verdict.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}) {
		t.Errorf("with git failing, the session whose directory is present = %+v, want it still reopenable", verdict)
	}
	if shown, err := cli.SessionShow(missing); err != nil || shown.Reopen != nil {
		t.Errorf("with git failing, showing the session whose worktree is gone = %+v (%v), want its row without a verdict", shown, err)
	}
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: missing}); err == nil || strings.Contains(err.Error(), "cannot be reopened") {
		t.Errorf("with git failing, reopening the session whose worktree is gone = %v, want the failure reported rather than a refusal", err)
	}
	if verdict := reopenVerdict(t, cli, lost); verdict.Reopenable || len(verdict.Actions) != 0 || !strings.Contains(protocol.Deref(verdict.Reason), "repository is gone") {
		t.Errorf("with git failing, the session whose repository is gone = %+v, want the refusal naming the repository", verdict)
	}
}

func closedReopenCodex(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, dir string) string {
	t.Helper()
	session := w.Spawn(app, fakeagent.Codex, dir)
	w.Launched(session)
	closeSession(t, cli, session, "done for now")
	awaitClosed(app, session)
	return session
}

func reopenVerdict(t *testing.T, cli *client.Client, session string) protocol.SessionReopen {
	t.Helper()
	shown, err := cli.SessionShow(session)
	if err != nil {
		t.Fatalf("session show %s: %v", session, err)
	}
	if shown.Reopen == nil {
		t.Fatalf("session show %s carries no reopen verdict", session)
	}
	return *shown.Reopen
}

func removeReopenRollout(t *testing.T, w *world, conversation string) {
	t.Helper()
	rollouts, err := filepath.Glob(filepath.Join(w.Dir, "toolhome", ".codex", "sessions", "*", "*", "*", "*-"+conversation+".jsonl"))
	if err != nil || len(rollouts) != 1 {
		t.Fatalf("rollout of %s = %v (%v), want exactly one", conversation, rollouts, err)
	}
	if err := os.Remove(rollouts[0]); err != nil {
		t.Fatal(err)
	}
}

func reopenRepoWithOrigin(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(root, "origin.git")
	runGit(t, root, "init", "-q", "--bare", "--initial-branch=main", origin)
	repo := filepath.Join(root, "repo")
	runGit(t, root, "clone", "-q", origin, repo)
	commitFile(t, repo, "README.md", "shop\n")
	runGit(t, repo, "push", "-q", "-u", "origin", "main")
	return repo
}

func reopenWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	worktree := filepath.Join(filepath.Dir(repo), "wt-"+strings.ReplaceAll(branch, "/", "-"))
	runGit(t, repo, "worktree", "add", "-q", "-b", branch, worktree)
	return worktree
}
