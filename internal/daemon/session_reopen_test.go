package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func newReopenRepo(t *testing.T) (repo, origin, root string) {
	t.Helper()
	root = t.TempDir()
	origin = filepath.Join(root, "origin.git")
	runGitDaemon(t, root, "init", "--bare", "--initial-branch=main", origin)

	seed := filepath.Join(root, "seed")
	runGitDaemon(t, root, "clone", origin, seed)
	runGitDaemon(t, seed, "commit", "--allow-empty", "-m", "init")
	runGitDaemon(t, seed, "push", "-u", "origin", "main")

	repo = filepath.Join(root, "repo")
	runGitDaemon(t, root, "clone", origin, repo)
	return attngit.CanonicalizePath(repo), origin, root
}

type reopenSession struct {
	ID         string
	Directory  string
	Branch     string
	Repo       string
	Agent      string
	Resume     string
	ClosedBy   string
	Reason     string
	CostCursor string
	NoIntent   bool
	Intent     *store.LaunchIntent
}

func closeReopenSession(t *testing.T, d *Daemon, session reopenSession) {
	t.Helper()
	now := protocol.TimestampNow().String()
	entry := &protocol.Session{
		ID: session.ID, Label: session.ID,
		Agent:     protocol.SessionAgent(session.Agent),
		Directory: session.Directory, WorkspaceID: "workspace-" + session.ID,
		State:      protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	}
	if session.Branch != "" {
		entry.Branch = protocol.Ptr(session.Branch)
	}
	if session.Repo != "" {
		entry.IsWorktree = protocol.Ptr(true)
		entry.MainRepo = protocol.Ptr(session.Repo)
	}
	d.store.Add(entry)
	if !session.NoIntent {
		intent := store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser}
		if session.Intent != nil {
			intent = *session.Intent
		}
		d.store.SetLaunchIntent(session.ID, intent)
	}
	if session.Resume != "" {
		d.persistResumeSessionID(session.ID, session.Resume)
	}
	if session.CostCursor != "" {
		if err := d.store.SetSessionCostCursor(session.ID, session.CostCursor); err != nil {
			t.Fatalf("set the cost cursor of %s: %v", session.ID, err)
		}
	}
	closedBy := session.ClosedBy
	if closedBy == "" {
		closedBy = store.SessionClosedByUser
	}
	d.closeSession(session.ID, store.SessionClose{By: closedBy, Reason: session.Reason})
	if !d.store.SessionClosed(session.ID) {
		t.Fatalf("session %s did not close into the ledger", session.ID)
	}
}

func decidedReopenVerdict(t *testing.T, d *Daemon, sessionID string) *sessionReopenVerdict {
	t.Helper()
	entry := d.store.SessionLedgerEntry(sessionID)
	if entry == nil {
		t.Fatalf("no ledger row for %s", sessionID)
	}
	verdict, err := d.resolveReopen(
		context.Background(), *entry, d.scheduledReopenGit(),
	)
	if err != nil {
		t.Fatalf("resolve reopen verdict for %s: %v", sessionID, err)
	}
	return &verdict
}

func actionNames(actions []protocol.SessionReopenAction) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		names = append(names, string(action))
	}
	return names
}

func wantReopenVerdict(
	t *testing.T, verdict *sessionReopenVerdict, reopenable bool, actions []protocol.SessionReopenAction,
) {
	t.Helper()
	if verdict.Reopenable != reopenable {
		t.Errorf("reopenable = %v, want %v (reason %q)", verdict.Reopenable, reopenable, verdict.Reason)
	}
	if !slices.Equal(actionNames(verdict.Actions), actionNames(actions)) {
		t.Errorf("actions = %v, want %v", actionNames(verdict.Actions), actionNames(actions))
	}
	if !reopenable && strings.TrimSpace(verdict.Reason) == "" {
		t.Error("a verdict that refuses a reopen carries no reason; an agent cannot act on that")
	}
}

func TestReopenVerdictOffersOnlyFreshStartWithoutItsLaunchContract(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	closeReopenSession(t, d, reopenSession{
		ID: "missing-contract", Directory: t.TempDir(), Agent: "codex", NoIntent: true,
	})

	verdict := decidedReopenVerdict(t, d, "missing-contract")
	wantReopenVerdict(t, verdict, false, []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace})
	if !strings.Contains(verdict.Reason, "launch contract") {
		t.Fatalf("reason = %q, want the missing launch contract named", verdict.Reason)
	}
	if _, err := d.reopenSession("missing-contract", protocol.SessionReopenActionStartFreshSamePlace, ""); err != nil {
		t.Fatal(err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.ResumeSessionID != "" {
		t.Fatalf("fresh spawn = %+v, %v; want a new conversation", spawn, ok)
	}
}

func TestReopenVerdictSendsARemoteSessionToItsOwnDaemon(t *testing.T) {
	endpoints := []protocol.EndpointInfo{
		{ID: "outpost-7", Name: "big-linux", Status: "connected"},
		{ID: "outpost-8", Name: "sleepy-linux", Status: "disconnected"},
	}
	cases := map[string]struct {
		endpointID string
		wantTail   string
	}{
		"reachable":   {endpointID: "outpost-7", wantTail: "reopen it there"},
		"unreachable": {endpointID: "outpost-8", wantTail: "retry when it is"},
		"forgotten":   {endpointID: "outpost-nobody-configured", wantTail: "retry when it is"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			verdict := &sessionReopenVerdict{
				SessionID: "remote-one",
				Execution: garden.Dispatch{HostKind: garden.HostRemote, EndpointID: tc.endpointID},
			}
			if decideReopenHost(verdict, endpoints) {
				t.Fatal("a remote session went on being decided on this daemon")
			}
			wantReopenVerdict(t, verdict, false, nil)
			if !strings.Contains(verdict.Reason, tc.endpointID) &&
				!strings.Contains(verdict.Reason, "big-linux") &&
				!strings.Contains(verdict.Reason, "sleepy-linux") {
				t.Errorf("reason = %q, want the host named", verdict.Reason)
			}
			if !strings.Contains(verdict.Reason, tc.wantTail) {
				t.Errorf("reason = %q, want it to end with %q", verdict.Reason, tc.wantTail)
			}
		})
	}
}

func TestReopenVerdictNamesAnAgentThatIsNotInstalled(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	closeReopenSession(t, d, reopenSession{
		ID: "gone-agent", Directory: t.TempDir(), Agent: "a-harness-nobody-installed", Resume: "conv-1",
	})

	verdict := decidedReopenVerdict(t, d, "gone-agent")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace})
	if !strings.Contains(verdict.Reason, "a-harness-nobody-installed") {
		t.Errorf("reason = %q, want the missing agent named", verdict.Reason)
	}
}

func TestReopenVerdictLabelsAGoneBranchThatWasMerged(t *testing.T) {
	d, repo, _ := closedWorktreeWithDeletedDirectory(t, "merged-branch", "feat/merged", true)
	runGitDaemon(t, repo, "push", "origin", "--delete", "feat/merged")
	runGitDaemon(t, repo, "fetch", "--prune", "origin")
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/merged")
	if _, err := d.store.RecordSessionPullRequest(store.SessionPullRequestRecord{
		SessionID: "merged-branch", PRID: "github.com:o/r#7", Repository: "github.com/o/r", Number: 7,
		URL: "https://github.com/o/r/pull/7",
	}, time.Now()); err != nil {
		t.Fatalf("record the session's pull request: %v", err)
	}
	if err := d.store.UpdateSessionPullRequestStatus("github.com:o/r#7", store.SessionPullRequestStatus{
		State: "merged", HeadBranch: "feat/merged",
	}, time.Now()); err != nil {
		t.Fatalf("mark the pull request merged: %v", err)
	}

	verdict := decidedReopenVerdict(t, d, "merged-branch")
	wantReopenVerdict(t, verdict, false, []protocol.SessionReopenAction{
		protocol.SessionReopenActionStartFreshDefaultBranch,
		protocol.SessionReopenActionStartFreshElsewhere,
	})
	if verdict.BranchState != branchStateMerged {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateMerged)
	}
	if !strings.Contains(verdict.Reason, "merged") {
		t.Errorf("reason = %q, want it to say the branch was merged", verdict.Reason)
	}
}

func closedWorktreeWithDeletedDirectory(
	t *testing.T, sessionID, branch string, pushed bool,
) (*Daemon, string, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-"+sessionID)
	repo, _, root := newReopenRepo(t)
	worktree := filepath.Join(root, "wt-"+sessionID)
	runGitDaemon(t, repo, "worktree", "add", "-b", branch, worktree)
	if pushed {
		runGitDaemon(t, worktree, "push", "-u", "origin", branch)
	}

	closeReopenSession(t, d, reopenSession{
		ID: sessionID, Directory: worktree, Branch: branch, Repo: repo,
		Agent: "codex", Resume: "conv-" + sessionID,
	})
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("delete the worktree directory: %v", err)
	}
	return d, repo, worktree
}

func TestReopenVerdictReopensAPluginConversationByCapability(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"resume": true})
	closeReopenSession(t, d, reopenSession{
		ID: "plugin-conv", Directory: t.TempDir(), Agent: "snipe", Resume: "snipe-conv-1",
	})

	verdict := decidedReopenVerdict(t, d, "plugin-conv")
	wantReopenVerdict(t, verdict, true, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen})
}

func TestReopenVerdictNamesAPluginThatDoesNotResume(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	client, done := startPluginPipe(t, d, "spawn-only-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "spawn-only", map[string]bool{})
	closeReopenSession(t, d, reopenSession{
		ID: "plugin-fresh", Directory: t.TempDir(), Agent: "spawn-only", Resume: "conv-1",
	})

	verdict := decidedReopenVerdict(t, d, "plugin-fresh")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace})
	if !strings.Contains(verdict.Reason, "spawn-only") || !strings.Contains(verdict.Reason, "resume") {
		t.Errorf("reason = %q, want the plugin named and the missing capability", verdict.Reason)
	}
}
