package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheWorktreeSurfaceNamesEachMergedSignal(t *testing.T) {
	pulls := newMergedPullRequests(t)
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop := sweepRepoOnGitHub(t)
	ancestor := createWorktree(t, app, shop, "feat-ancestor")
	tree := createWorktree(t, app, shop, "feat-tree")
	commitFile(t, tree, "shared.txt", "shared\n")
	if err := os.WriteFile(filepath.Join(shop, "shared.txt"), []byte("shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, shop, "add", "shared.txt")
	runGit(t, shop, "commit", "-m", "the same change, landed on main")
	pr := createWorktree(t, app, shop, "feat-pr")
	prHead := commitFile(t, pr, "pr.txt", "pr\n")
	unmerged := createWorktree(t, app, shop, "feat-unmerged")
	commitFile(t, unmerged, "only-here.txt", "only here\n")
	runGit(t, shop, "update-ref", "refs/remotes/origin/main", "main")
	pulls.list(mergedPullRequest{Number: 7, MergedAt: "2026-08-06T10:00:00Z", Head: ref{Ref: "feat-pr", SHA: prHead}, Base: ref{Ref: "main"}})
	for _, path := range []string{ancestor, tree, pr, unmerged} {
		sweepDirty(t, path)
	}

	refreshWorktrees(t, cli)
	close(pulls.awaitSweepAsking(t))
	for path, want := range map[string]string{ancestor: "ancestor", tree: "tree", pr: "pull_request", unmerged: ""} {
		observed := sweepAwaitState(app, path, func(wt protocol.Worktree) bool { return wt.ObservedAt != nil })
		if got := protocol.Deref(observed.MergedSignal); got != want {
			t.Errorf("%s reads as merged by %q; want %q", filepath.Base(path), got, want)
		}
	}
}

func TestTheSweepKeepsEachWorktreeItMustAndSaysWhy(t *testing.T) {
	pulls := newMergedPullRequests(t)
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	shop := sweepRepoOnGitHub(t)
	runGit(t, shop, "update-ref", "refs/remotes/origin/main", "main")

	pinned := createWorktree(t, app, shop, "feat-pinned")
	live := createWorktree(t, app, shop, "feat-live")
	session := w.Spawn(app, fakeagent.Codex, live)
	dirty := createWorktree(t, app, shop, "feat-dirty")
	sweepDirty(t, dirty)
	stashed := createWorktree(t, app, shop, "feat-stashed")
	if err := os.WriteFile(filepath.Join(stashed, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, stashed, "stash", "push", "-m", "keep me")
	detached := createWorktree(t, app, shop, "feat-detached")
	commitFile(t, detached, "detached.txt", "detached\n")
	runGit(t, detached, "checkout", "--detach", "HEAD")
	unmerged := createWorktree(t, app, shop, "feat-unmerged")
	commitFile(t, unmerged, "only-here.txt", "only here\n")
	unpushed := createWorktree(t, app, shop, "feat-unpushed")
	mergedHead := commitFile(t, unpushed, "merged.txt", "merged\n")
	commitFile(t, unpushed, "after.txt", "after the merge\n")
	pulls.list(mergedPullRequest{Number: 8, MergedAt: "2026-08-06T10:00:00Z", Head: ref{Ref: "feat-unpushed", SHA: mergedHead}, Base: ref{Ref: "main"}})
	sweepKeep(t, cli, pinned, true)
	sweepAwaitState(app, pinned, func(wt protocol.Worktree) bool { return protocol.Deref(wt.SweepStatus) == "pinned" })

	refreshWorktrees(t, cli)
	close(pulls.awaitSweepAsking(t))
	for _, row := range []struct {
		path, status, reason string
	}{
		{live, "kept_live_session", session},
		{dirty, "kept_dirty", "uncommitted"},
		{stashed, "kept_dirty", "stash"},
		{detached, "kept_detached", "detached HEAD"},
		{unmerged, "kept_unmerged", "no merged signal"},
		{unpushed, "kept_unpushed", "does not account for"},
	} {
		kept := sweepAwaitState(app, row.path, func(wt protocol.Worktree) bool { return wt.SweepStatus != nil })
		if protocol.Deref(kept.SweepStatus) != row.status || !strings.Contains(protocol.Deref(kept.SweepReason), row.reason) {
			t.Errorf("the sweep decided %s is %s (%s); want %s naming %q", filepath.Base(row.path), protocol.Deref(kept.SweepStatus), protocol.Deref(kept.SweepReason), row.status, row.reason)
		}
	}
	if listed := sweepListed(t, cli, shop, pinned); !protocol.Deref(listed.Pinned) || protocol.Deref(listed.SweepStatus) != "pinned" || !strings.Contains(protocol.Deref(listed.SweepReason), "kept forever") {
		t.Errorf("after the sweep the pinned worktree reads %+v; want it still pinned", listed)
	}

	sweepKeep(t, cli, pinned, false)
	refreshWorktrees(t, cli)
	close(pulls.awaitSweepAsking(t))
	swept := sweepAwaitSwept(app, pinned)
	if swept.Action != "removed" {
		t.Errorf("once unpinned the sweep recorded %s for the worktree; want it removed", swept.Action)
	}
	for _, path := range []string{live, dirty, stashed, detached, unmerged, unpushed} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the sweep removed %s, which it had to keep: %v", filepath.Base(path), err)
		}
	}
}

func TestTheSweepReclaimsWhatNoSessionHolds(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	shop := newRepo(t, "shop")
	reclaimed := createWorktree(t, app, shop, "feat-reclaimed")
	held := createWorktree(t, app, shop, "feat-held")
	session := w.Spawn(app, fakeagent.Codex, held)
	locked := createWorktree(t, app, shop, "feat-locked")
	runGit(t, shop, "worktree", "lock", locked)
	setSetting(t, app, "worktree_sweep_enabled", "false")

	refreshWorktrees(t, cli)
	for _, row := range []struct{ path, status, reason string }{
		{reclaimed, "scheduled", "the sweep is off"},
		{held, "kept_live_session", session},
		{locked, "", "locked"},
	} {
		kept := sweepAwaitState(app, row.path, func(wt protocol.Worktree) bool { return wt.SweepReason != nil })
		if protocol.Deref(kept.SweepStatus) != row.status || !strings.Contains(protocol.Deref(kept.SweepReason), row.reason) {
			t.Errorf("with the sweep off %s reads %s (%s); want %q naming %q", filepath.Base(row.path), protocol.Deref(kept.SweepStatus), protocol.Deref(kept.SweepReason), row.status, row.reason)
		}
	}

	setSetting(t, app, "worktree_sweep_enabled", "true")
	refreshWorktrees(t, cli)
	swept := sweepAwaitSwept(app, reclaimed)
	if swept.Action != "removed" || !strings.Contains(protocol.Deref(swept.Reason), "merged") {
		t.Errorf("the sweep recorded %s (%s); want the worktree removed as merged", swept.Action, protocol.Deref(swept.Reason))
	}
	testworld.Await(app, protocol.EventWorktreeDeleted, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == reclaimed
	})
	if _, err := os.Stat(reclaimed); !os.IsNotExist(err) {
		t.Errorf("the reclaimed worktree is still on disk: %v", err)
	}
	for _, path := range []string{held, locked} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the sweep removed %s: %v", filepath.Base(path), err)
		}
	}
	if log, err := cli.WorktreeSweepLog(shop, 10); err != nil || !slices.Equal(sweptPaths(log.Entries), []string{reclaimed}) {
		t.Errorf("the sweep log = %+v, %v; want only the reclaimed worktree", log, err)
	}
	if listed := sweepListed(t, cli, shop, held); protocol.Deref(listed.SweepStatus) != "kept_live_session" || !strings.Contains(protocol.Deref(listed.SweepReason), session) {
		t.Errorf("the held worktree reads %+v; want it kept for %s", listed, session)
	}
}

func TestTheWorktreeListFollowsGit(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop := newRepo(t, "shop")
	createWorktree(t, app, shop, "feat-known")
	nested := filepath.Join(shop, ".claude", "worktrees", "agent-1")
	runGit(t, shop, "worktree", "add", "-b", "agent/one", nested)

	refreshWorktrees(t, cli)
	if adopted := sweepAwaitState(app, nested, func(protocol.Worktree) bool { return true }); adopted.Branch != "agent/one" || adopted.MainRepo != shop {
		t.Errorf("the worktree git added under .claude/worktrees reads %+v; want branch agent/one of %s", adopted, shop)
	}

	runGit(t, shop, "worktree", "remove", nested)
	refreshWorktrees(t, cli)
	testworld.Await(app, protocol.EventWorktreeDeleted, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == nested
	})
	if listed, err := cli.WorktreeList(shop, 0); err != nil || slices.Contains(worktreeCreatePaths(listed.Worktrees), nested) {
		t.Errorf("the worktree list after git removed it = %+v, %v; want it gone", listed, err)
	}
}

func TestAMergedWorktreeIsScheduledForTheEndOfItsIdleWindow(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	shop := newRepo(t, "shop")
	path := createWorktree(t, app, shop, "feat-done")
	created := sweepCreatedAt(t, app, path)

	for _, row := range []struct {
		idleDays string
		days     int
	}{
		{"", 14},
		{"3", 3},
		{"not a number", 14},
	} {
		t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", row.idleDays)
		w.restart()
		app := w.App()
		refreshWorktrees(t, w.Client())
		scheduled := sweepAwaitState(app, path, func(wt protocol.Worktree) bool {
			return strings.Contains(protocol.Deref(wt.SweepReason), fmt.Sprintf("of %d days", row.days))
		})
		want := created.Add(time.Duration(row.days) * 24 * time.Hour)
		at, err := time.Parse(time.RFC3339, protocol.Deref(scheduled.SweepAt))
		if protocol.Deref(scheduled.SweepStatus) != "scheduled" || err != nil || !at.Equal(want) {
			t.Errorf("with ATTN_WORKTREE_SWEEP_IDLE_DAYS=%q the merged worktree reads %s at %s (%s); want scheduled for %s",
				row.idleDays, protocol.Deref(scheduled.SweepStatus), protocol.Deref(scheduled.SweepAt), protocol.Deref(scheduled.SweepReason), want.Format(time.RFC3339))
		}
	}
}

func TestTheSweepActsOnlyOnWhatItCouldSee(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	type breakage func(t *testing.T, app *testworld.Peer, cli *client.Client, pulls *mergedPullRequests, shop, path string) string
	for _, row := range []struct {
		name    string
		github  bool
		reason  string
		arrange breakage
	}{
		{"a repository it could not list", false, "could not be refreshed", func(t *testing.T, app *testworld.Peer, cli *client.Client, _ *mergedPullRequests, shop, path string) string {
			setSetting(t, app, "worktree_sweep_enabled", "false")
			refreshWorktrees(t, cli)
			sweepAwaitState(app, path, func(wt protocol.Worktree) bool {
				return strings.Contains(protocol.Deref(wt.SweepReason), "the sweep is off")
			})
			setSetting(t, app, "worktree_sweep_enabled", "true")
			commitFile(t, path, "new-work.txt", "not on any branch yet\n")
			if err := os.Rename(filepath.Join(shop, ".git"), filepath.Join(shop, ".git-gone")); err != nil {
				t.Fatal(err)
			}
			return "new-work.txt"
		}},
		{"stashes it could not list", false, "could not be refreshed", func(t *testing.T, app *testworld.Peer, cli *client.Client, _ *mergedPullRequests, shop, path string) string {
			if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, path, "stash", "push", "-m", "keep me")
			refreshWorktrees(t, cli)
			sweepAwaitState(app, path, func(wt protocol.Worktree) bool { return strings.Contains(protocol.Deref(wt.SweepReason), "stash") })
			if err := os.WriteFile(filepath.Join(shop, ".git", "refs", "stash"), []byte(strings.Repeat("0", 39)+"1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return "README.md"
		}},
		{"an integration branch it could not resolve", true, "last refresh failed", func(t *testing.T, _ *testworld.Peer, _ *client.Client, pulls *mergedPullRequests, shop, path string) string {
			mergedHead := commitFile(t, path, "merged.txt", "merged\n")
			commitFile(t, path, "after.txt", "reused after the merge\n")
			runGit(t, shop, "remote", "add", "origin", "https://github.test/acme/shop.git")
			pulls.list(mergedPullRequest{Number: 11, MergedAt: "2026-08-06T10:00:00Z", Head: ref{Ref: "feat-work", SHA: mergedHead}, Base: ref{Ref: "gone"}})
			return "after.txt"
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			var pulls *mergedPullRequests
			if row.github {
				pulls = newMergedPullRequests(t)
			}
			w := newWorld(t)
			app, cli := w.App(), w.Client()
			shop := newRepo(t, "shop")
			path := createWorktree(t, app, shop, "feat-work")
			work := row.arrange(t, app, cli, pulls, shop, path)

			refreshWorktrees(t, cli)
			if pulls != nil {
				close(pulls.awaitSweepAsking(t))
			}
			undecided := sweepAwaitState(app, path, func(wt protocol.Worktree) bool {
				return strings.Contains(protocol.Deref(wt.SweepReason), row.reason)
			})
			if undecided.SweepStatus != nil {
				t.Errorf("the worktree reads %s (%s); want it undecided", protocol.Deref(undecided.SweepStatus), protocol.Deref(undecided.SweepReason))
			}
			if _, err := os.Stat(filepath.Join(path, work)); err != nil {
				t.Errorf("the work in the worktree is gone: %v", err)
			}
			for _, e := range app.Received() {
				if e.Event == protocol.EventWorktreeSwept {
					t.Errorf("the sweep acted on what it could not see: %+v", e.SweepEntry)
				}
			}
		})
	}
}

func TestTheWorktreeSurfaceNeverPutsNullWhereTheAppExpectsAnArray(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	list := testworld.Request(app, protocol.WorktreeListMessage{Cmd: protocol.CmdWorktreeList, RequestID: protocol.Ptr("list")},
		protocol.EventWorktreeListResult, func(json.RawMessage) bool { return true })
	log := testworld.Request(app, protocol.WorktreeSweepLogMessage{Cmd: protocol.CmdWorktreeSweepLog, RequestID: protocol.Ptr("log")},
		protocol.EventWorktreeSweepLogResult, func(json.RawMessage) bool { return true })
	for _, want := range []struct {
		frame json.RawMessage
		field string
	}{
		{list, `"worktrees":[]`},
		{list, `"repositories":[]`},
		{log, `"entries":[]`},
	} {
		if !strings.Contains(string(want.frame), want.field) {
			t.Errorf("the app received %s; want %s in it", want.frame, want.field)
		}
	}
}

func sweepRepoOnGitHub(t *testing.T) string {
	t.Helper()
	shop := newRepo(t, "shop")
	runGit(t, shop, "remote", "add", "origin", "https://github.test/acme/shop.git")
	return shop
}

func sweepDirty(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sweepKeep(t *testing.T, cli *client.Client, path string, keep bool) {
	t.Helper()
	if _, err := cli.WorktreeKeep(path, keep); err != nil {
		t.Fatalf("keep %s = %v: %v", path, keep, err)
	}
}

func sweepAwaitState(app *testworld.Peer, path string, match func(protocol.Worktree) bool) protocol.Worktree {
	app.T.Helper()
	return testworld.Await(app, protocol.EventWorktreeStateChanged, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == path && match(e.Worktrees[0])
	}).Worktrees[0]
}

func sweepAwaitSwept(app *testworld.Peer, path string) protocol.WorktreeSweepEntry {
	app.T.Helper()
	return *testworld.Await(app, protocol.EventWorktreeSwept, func(e protocol.WebSocketEvent) bool {
		return e.SweepEntry != nil && e.SweepEntry.Path == path
	}).SweepEntry
}

func sweepListed(t *testing.T, cli *client.Client, repo, path string) protocol.Worktree {
	t.Helper()
	listed, err := cli.WorktreeList(repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range listed.Worktrees {
		if wt.Path == path {
			return wt
		}
	}
	t.Fatalf("%s is not in the worktree list %+v", path, listed.Worktrees)
	return protocol.Worktree{}
}

func sweepCreatedAt(t *testing.T, app *testworld.Peer, path string) time.Time {
	t.Helper()
	created := testworld.Await(app, protocol.EventWorktreeCreated, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == path
	}).Worktrees[0]
	at, err := time.Parse(time.RFC3339, protocol.Deref(created.CreatedAt))
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestRemovingASeedsWorktreeNotesItOnTheSeed(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	shop := newRepo(t, "shop")
	caller := w.Spawn(app, fakeagent.Codex, shop)
	dispatch := func(text, branch string) *protocol.DelegateResult {
		request := delegateFrom(caller, shop, text, fakeagent.Codex)
		request.Label = protocol.Ptr(branch)
		request.Checkout = delegateNewWorktree(branch, "main")
		delegated, err := cli.Delegate(request)
		if err != nil {
			t.Fatalf("dispatching %q: %v", text, err)
		}
		return delegated
	}
	finished := dispatch("Finish the checkout", "feat/finished")
	open := dispatch("Keep the ledger going", "feat/open")
	if _, err := cli.SeedTransition(finished.SessionID, finished.SeedID, "harvest", "done", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("harvesting %s: %v", finished.SeedID, err)
	}
	for _, delegated := range []*protocol.DelegateResult{finished, open} {
		if _, err := cli.AgentClose(delegated.SessionID, caller, "done for now"); err != nil {
			t.Fatalf("closing %s: %v", delegated.SessionID, err)
		}
	}

	refreshWorktrees(t, cli)
	if swept := sweepAwaitSwept(app, finished.Directory); swept.Action != "removed" {
		t.Errorf("the sweep recorded %s for the harvested seed's worktree; want it removed", swept.Action)
	}
	if kept := sweepAwaitState(app, open.Directory, func(wt protocol.Worktree) bool { return wt.SweepStatus != nil }); protocol.Deref(kept.SweepStatus) != "kept_open_seed" || !strings.Contains(protocol.Deref(kept.SweepReason), open.SeedID) {
		t.Errorf("the open seed's worktree reads %s (%s); want it kept for %s", protocol.Deref(kept.SweepStatus), protocol.Deref(kept.SweepReason), open.SeedID)
	}

	if err := cli.DeleteWorktree(open.Directory, false); err != nil {
		t.Fatalf("deleting the open seed's worktree by hand: %v", err)
	}
	log, err := cli.WorktreeSweepLog(shop, 10)
	if err != nil || len(log.Entries) != 2 || log.Entries[0].Path != open.Directory || log.Entries[0].Action != "deleted" {
		t.Errorf("the sweep log = %+v, %v; want the deletion by hand on top of the sweep's removal", log, err)
	}
	for _, row := range []struct {
		delegated *protocol.DelegateResult
		branch    string
		action    string
	}{
		{finished, "feat/finished", "attn removed the worktree"},
		{open, "feat/open", "attn deleted the worktree"},
	} {
		notes, err := cli.SeedNotes("", row.delegated.SeedID, 20)
		if err != nil {
			t.Fatal(err)
		}
		var note string
		for _, n := range notes.Notes {
			if strings.Contains(n.Body, row.action) {
				note = n.Body
			}
		}
		for _, want := range []string{row.action, row.delegated.Directory, row.branch, shop} {
			if !strings.Contains(note, want) {
				t.Errorf("seed %s holds no note naming %q; its notes are %+v", row.delegated.SeedID, want, notes.Notes)
			}
		}
	}
}
