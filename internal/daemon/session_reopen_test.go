package daemon

import (
	"os"
	"path/filepath"
	"reflect"
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
	ProfileID  string
}

func closeReopenSession(t *testing.T, d *Daemon, session reopenSession) {
	t.Helper()
	now := protocol.TimestampNow().String()
	if session.ProfileID == "" {
		session.ProfileID = defaultProfileID(t, d.store)
	}
	entry := &protocol.Session{
		ID: session.ID, Label: session.ID,
		Agent:     protocol.SessionAgent(session.Agent),
		Directory: session.Directory, ProfileID: session.ProfileID,
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
	verdict, found := d.reopenVerdict(sessionID)
	if !found {
		t.Fatalf("no ledger row for %s", sessionID)
	}
	if !verdict.Checking {
		return verdict
	}
	<-d.inspectBranchInBackground(sessionID, verdict.Execution.RepositoryRoot, verdict.Execution.Branch)
	verdict, found = d.reopenVerdict(sessionID)
	if !found {
		t.Fatalf("no ledger row for %s after its branch check", sessionID)
	}
	if verdict.Checking {
		t.Fatalf("%s is still checking after its branch check landed", sessionID)
	}
	return verdict
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

func TestReopenVerdictSendsALiveSessionBackToItsPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "running", t.TempDir())

	verdict := decidedReopenVerdict(t, d, "running")
	if !verdict.Live {
		t.Fatal("a registered session reads as closed")
	}
	wantReopenVerdict(t, verdict, false, nil)
	if !strings.Contains(verdict.Reason, "focus it") {
		t.Errorf("reason = %q, want it to send the caller to the running session", verdict.Reason)
	}
}

func TestReopeningALiveSessionChangesNothing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "running", t.TempDir())

	outcome, err := d.reopenSession("running", "", "", profileDestination{})
	if err != nil {
		t.Fatalf("reopenSession(live) error = %v", err)
	}
	if !outcome.AlreadyRunning {
		t.Error("reopening a live session did not report it as already running")
	}
}

func TestReopeningASessionWithNoLedgerRowSaysWhereToLookInstead(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))

	_, err := d.reopenSession("never-ran", "", "", profileDestination{})
	if err == nil {
		t.Fatal("reopening an unknown session id succeeded")
	}
	for _, want := range []string{"never-ran", "ledger", "seed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
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
	if _, err := d.reopenSession("missing-contract", protocol.SessionReopenActionStartFreshSamePlace, "", profileDestination{}); err != nil {
		t.Fatal(err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.ResumeSessionID != "" {
		t.Fatalf("fresh spawn = %+v, %v; want a new conversation", spawn, ok)
	}
}

func TestReopenReplaysTheLedgerLaunchContract(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	autoMode := false
	intent := store.LaunchIntent{
		AutoMode:      &autoMode,
		ApprovalRoute: launchcontract.ApprovalRouteUser,
		Executable:    "/opt/codex",
		Model:         "gpt-ledger",
		Effort:        "high",
	}
	writeCodexRolloutFixture(t, "codex-ledger-conversation")
	closeReopenSession(t, d, reopenSession{
		ID: "ledger-contract", Directory: t.TempDir(), Agent: "codex",
		Resume: "codex-ledger-conversation", Intent: &intent,
	})

	if _, err := d.reopenSession("ledger-contract", protocol.SessionReopenActionReopen, "", profileDestination{}); err != nil {
		t.Fatal(err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("backend Spawn not called")
	}
	if spawn.ResumeSessionID != "codex-ledger-conversation" || spawn.Executable != "/opt/codex" || spawn.Model != "gpt-ledger" || spawn.Effort != "high" {
		t.Fatalf("spawn = %+v, want the saved transcript, executable, model and effort", spawn)
	}
	if got, ok := d.store.LaunchIntent("ledger-contract"); !ok || !reflect.DeepEqual(got, intent) {
		t.Fatalf("launch intent after reopen = %+v, %v; want %+v", got, ok, intent)
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

func TestALocalSessionIsDecidedHere(t *testing.T) {
	verdict := &sessionReopenVerdict{SessionID: "local", Execution: garden.Dispatch{HostKind: garden.HostLocal}}
	if !decideReopenHost(verdict, nil) {
		t.Fatalf("a local session stopped at the host check: %q", verdict.Reason)
	}
}

func TestReopenVerdictOffersAFreshStartWhenTheConversationIsGone(t *testing.T) {
	cases := map[string]struct {
		resume  string
		fixture bool
		want    string
	}{
		"resume id unknown":  {resume: "", fixture: false, want: "nothing to resume"},
		"transcript missing": {resume: "conv-vanished", fixture: false, want: "no longer in codex's storage"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
			if tc.fixture {
				writeCodexRolloutFixture(t, tc.resume)
			} else {
				writeCodexRolloutFixture(t, "some-other-conversation")
			}
			directory := t.TempDir()
			closeReopenSession(t, d, reopenSession{
				ID: "no-conversation", Directory: directory, Agent: "codex", Resume: tc.resume,
			})

			verdict := decidedReopenVerdict(t, d, "no-conversation")
			wantReopenVerdict(t, verdict, false,
				[]protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace})
			if !strings.Contains(verdict.Reason, tc.want) {
				t.Errorf("reason = %q, want it to contain %q", verdict.Reason, tc.want)
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

func TestReopenVerdictReopensAPresentDirectory(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-present")
	repo, _, root := newReopenRepo(t)
	worktree := filepath.Join(root, "wt-present")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/present", worktree)

	closeReopenSession(t, d, reopenSession{
		ID: "present", Directory: worktree, Branch: "feat/present", Repo: repo,
		Agent: "codex", Resume: "conv-present",
	})

	verdict := decidedReopenVerdict(t, d, "present")
	wantReopenVerdict(t, verdict, true, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen})
	if verdict.Warning != "" {
		t.Errorf("warning = %q, want none while the directory is on its own branch", verdict.Warning)
	}
}

func TestReopenVerdictWarnsWhenTheDirectorySwitchedBranch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-switched")
	repo, _, root := newReopenRepo(t)
	worktree := filepath.Join(root, "wt-switched")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/switched", worktree)

	closeReopenSession(t, d, reopenSession{
		ID: "switched", Directory: worktree, Branch: "feat/switched", Repo: repo,
		Agent: "codex", Resume: "conv-switched",
	})
	runGitDaemon(t, worktree, "switch", "-c", "feat/somewhere-else")

	verdict := decidedReopenVerdict(t, d, "switched")
	wantReopenVerdict(t, verdict, true, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen})
	if !strings.Contains(verdict.Warning, "feat/somewhere-else") {
		t.Errorf("warning = %q, want the branch the directory is on now", verdict.Warning)
	}
}

func TestReopenVerdictShowsADirectoryItCannotOpen(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-unreadable")
	notADirectory := filepath.Join(t.TempDir(), "checkout")
	if err := os.WriteFile(notADirectory, []byte("this is a file"), 0o644); err != nil {
		t.Fatalf("write the stand-in for an unreadable directory: %v", err)
	}

	closeReopenSession(t, d, reopenSession{
		ID: "unreadable", Directory: notADirectory, Agent: "codex", Resume: "conv-unreadable",
	})

	verdict := decidedReopenVerdict(t, d, "unreadable")
	wantReopenVerdict(t, verdict, false, nil)
	if !strings.Contains(verdict.Reason, "cannot be opened") {
		t.Errorf("reason = %q, want it to say the directory cannot be opened", verdict.Reason)
	}
}

func TestReopenVerdictOffersAnotherPlaceWhenThePlainDirectoryIsGone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-plain")
	gone := filepath.Join(t.TempDir(), "deleted")

	closeReopenSession(t, d, reopenSession{
		ID: "plain-gone", Directory: gone, Agent: "codex", Resume: "conv-plain",
	})

	verdict := decidedReopenVerdict(t, d, "plain-gone")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere})
	if !strings.Contains(verdict.Reason, "not a worktree attn can put back") {
		t.Errorf("reason = %q, want it to say the directory was not a worktree", verdict.Reason)
	}
}

func TestReopenVerdictRefusesWhenTheWorktreesRepositoryIsGoneToo(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-norepo")
	root := t.TempDir()

	closeReopenSession(t, d, reopenSession{
		ID: "no-repo", Directory: filepath.Join(root, "wt"), Branch: "feat/x",
		Repo: filepath.Join(root, "repo-that-never-existed"), Agent: "codex", Resume: "conv-norepo",
	})

	verdict := decidedReopenVerdict(t, d, "no-repo")
	wantReopenVerdict(t, verdict, false, nil)
	if !strings.Contains(verdict.Reason, "repository") {
		t.Errorf("reason = %q, want the missing repository named", verdict.Reason)
	}
}

func TestReopenVerdictOffersToPutABranchStillHereBackInPlace(t *testing.T) {
	d, repo, worktree := closedWorktreeWithDeletedDirectory(t, "local-branch", "feat/local", false)
	_ = repo

	verdict := decidedReopenVerdict(t, d, "local-branch")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionRecreateWorktreeAndReopen})
	if verdict.BranchState != branchStateLocal {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateLocal)
	}
	if verdict.RecreatePath != attngit.CanonicalizePath(worktree) {
		t.Errorf("recreate path = %q, want the saved worktree %q", verdict.RecreatePath, worktree)
	}
}

func TestReopenVerdictOffersToFetchABranchOnlyOnTheRemote(t *testing.T) {
	d, repo, _ := closedWorktreeWithDeletedDirectory(t, "remote-branch", "feat/remote", true)
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/remote")

	verdict := decidedReopenVerdict(t, d, "remote-branch")
	wantReopenVerdict(t, verdict, false,
		[]protocol.SessionReopenAction{protocol.SessionReopenActionFetchRecreateAndReopen})
	if verdict.BranchState != branchStateRemoteOnly {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateRemoteOnly)
	}
	if !strings.Contains(verdict.Reason, "origin") {
		t.Errorf("reason = %q, want the remote carrying the branch named", verdict.Reason)
	}
}

func TestReopenVerdictOffersTheDefaultBranchWhenNothingCarriesTheBranch(t *testing.T) {
	d, repo, _ := closedWorktreeWithDeletedDirectory(t, "gone-branch", "feat/gone", false)
	runGitDaemon(t, repo, "worktree", "prune")
	runGitDaemon(t, repo, "branch", "-D", "feat/gone")

	verdict := decidedReopenVerdict(t, d, "gone-branch")
	wantReopenVerdict(t, verdict, false, []protocol.SessionReopenAction{
		protocol.SessionReopenActionStartFreshDefaultBranch,
		protocol.SessionReopenActionStartFreshElsewhere,
	})
	if verdict.BranchState != branchStateGone {
		t.Errorf("branch state = %q, want %q", verdict.BranchState, branchStateGone)
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

func TestReopenVerdictLandsInTheRecordedProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-profile")
	closeReopenSession(t, d, reopenSession{
		ID: "in-profile", Directory: t.TempDir(), Agent: "codex", Resume: "conv-profile",
	})

	verdict := decidedReopenVerdict(t, d, "in-profile")
	if verdict.ProfileID != defaultProfileID(t, d.store) || verdict.ProfileDeleted {
		t.Errorf("profile = %q (deleted %v), want the recorded default profile", verdict.ProfileID, verdict.ProfileDeleted)
	}
}

func TestReopenVerdictNamesADeletedProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-gone-profile")
	work := createTestProfile(t, d.store, "Work")
	closeReopenSession(t, d, reopenSession{
		ID: "gone-profile", Directory: t.TempDir(), Agent: "codex", Resume: "conv-gone-profile", ProfileID: work.ID,
	})
	deleteTestProfile(t, d.store, work.ID, defaultProfileID(t, d.store))

	verdict := decidedReopenVerdict(t, d, "gone-profile")
	if verdict.ProfileID != work.ID || !verdict.ProfileDeleted {
		t.Errorf("profile = %q (deleted %v), want the deleted Work profile named", verdict.ProfileID, verdict.ProfileDeleted)
	}
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
