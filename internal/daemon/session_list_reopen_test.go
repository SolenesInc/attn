package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func sessionListResult(t *testing.T, d *Daemon, msg protocol.SessionListMessage) protocol.SessionListResult {
	t.Helper()
	msg.Cmd = protocol.CmdSessionList
	server, client := net.Pipe()
	defer client.Close()
	go d.handleConnection(server)
	if err := json.NewEncoder(client).Encode(msg); err != nil {
		t.Fatalf("encode session_list: %v", err)
	}
	var response protocol.Response
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatalf("decode session_list response: %v", err)
	}
	if !response.Ok || response.SessionListResult == nil {
		t.Fatalf("session_list failed: %+v", response)
	}
	return *response.SessionListResult
}

func listedVerdict(t *testing.T, result protocol.SessionListResult, sessionID string) protocol.SessionReopen {
	t.Helper()
	for _, entry := range result.Reopen {
		if entry.SessionID == sessionID {
			return entry.Reopen
		}
	}
	t.Fatalf("the page carries no verdict for %s: %+v", sessionID, result.Reopen)
	return protocol.SessionReopen{}
}

func closeWorktreeRow(t *testing.T, d *Daemon, repo, root, sessionID, branch string) {
	t.Helper()
	writeCodexRolloutFixture(t, "conv-"+sessionID)
	worktree := filepath.Join(root, "wt-"+sessionID)
	runGitDaemon(t, repo, "worktree", "add", "-b", branch, worktree)
	closeReopenSession(t, d, reopenSession{
		ID: sessionID, Directory: worktree, Branch: branch, Repo: repo,
		Agent: "codex", Resume: "conv-" + sessionID,
	})
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("delete the worktree directory of %s: %v", sessionID, err)
	}
}

func TestAPageCarriesAVerdictForEveryClosedRowWhenItAsks(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "paged-one", "feat/paged-one")
	closeWorktreeRow(t, d, repo, root, "paged-two", "feat/paged-two")
	addLedgerTestSession(t, d, "still-running", t.TempDir())

	page := sessionListResult(t, d, protocol.SessionListMessage{
		All: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	})

	if len(page.Reopen) != 2 {
		t.Fatalf("the page carries %d verdicts, want one for each of the two closed rows: %+v",
			len(page.Reopen), page.Reopen)
	}
	closedIDs := make([]string, 0, 2)
	for _, entry := range page.Entries {
		if protocol.Deref(entry.ClosedAt) != "" {
			closedIDs = append(closedIDs, entry.ID)
		}
	}
	for i, sessionID := range closedIDs {
		if page.Reopen[i].SessionID != sessionID {
			t.Errorf("verdict %d is for %s, want row-order session %s", i, page.Reopen[i].SessionID, sessionID)
		}
	}
	for _, sessionID := range []string{"paged-one", "paged-two"} {
		if verdict := listedVerdict(t, page, sessionID); len(verdict.Actions) == 0 {
			t.Errorf("%s is offered no action at all: %+v", sessionID, verdict)
		}
	}
	for _, entry := range page.Reopen {
		if entry.SessionID == "still-running" {
			t.Error("a live row was judged; the surface renders it without a verdict")
		}
	}
}

func TestAListedVerdictIsTheOneAShowWouldGive(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "agreed", "feat/agreed")
	listed := listedVerdict(t, sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	}), "agreed")
	shown := sessionShowResult(t, d, "agreed").Reopen

	if shown == nil {
		t.Fatal("session_show carried no verdict for a closed row")
	}
	if listed.Reopenable != shown.Reopenable ||
		protocol.Deref(listed.Reason) != protocol.Deref(shown.Reason) ||
		protocol.Deref(listed.BranchState) != protocol.Deref(shown.BranchState) ||
		listed.DirectoryState != shown.DirectoryState ||
		len(listed.Actions) != len(shown.Actions) {
		t.Errorf("session_list carried %+v, session_show carried %+v; they must agree", listed, *shown)
	}
}

func TestARowWhoseRepositoryIsNoLongerGitStillListsAndShows(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "healthy", "feat/healthy")
	writeCodexRolloutFixture(t, "conv-not-git")
	closeReopenSession(t, d, reopenSession{
		ID: "not-git", Directory: filepath.Join(t.TempDir(), "missing"), Branch: "feat/not-git",
		Repo: t.TempDir(), Agent: "codex", Resume: "conv-not-git",
	})

	page := sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	})
	if len(page.Entries) != 2 {
		t.Fatalf("the page lists %d rows, want both", len(page.Entries))
	}
	listedVerdict(t, page, "healthy")
	for _, entry := range page.Reopen {
		if entry.SessionID == "not-git" {
			t.Fatalf("the uninspectable row carries a verdict: %+v", entry.Reopen)
		}
	}
	if shown := sessionShowResult(t, d, "not-git"); shown.Entry.ID != "not-git" || shown.Reopen != nil {
		t.Fatalf("session_show = %+v, want the entry without a verdict", shown)
	}
}

func TestAPageWithoutTheAskCarriesNoVerdicts(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "unjudged", "feat/unjudged")

	page := sessionListResult(t, d, protocol.SessionListMessage{Closed: protocol.Ptr(true)})

	if len(page.Entries) != 1 {
		t.Fatalf("the page holds %d rows, want the one closed session", len(page.Entries))
	}
	if len(page.Reopen) != 0 {
		t.Errorf("the page carries %d verdicts nobody asked for: %+v", len(page.Reopen), page.Reopen)
	}
	d.reopenGitMu.Lock()
	shared := d.reopenBranches
	d.reopenGitMu.Unlock()
	if shared != nil {
		t.Error("an unasked page initialized reopen branch inspection state")
	}
}

func TestReadingAPageTwiceRetainsNoCompletedBranchInspection(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "twice", "feat/twice")

	page := protocol.SessionListMessage{Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true)}
	sessionListResult(t, d, page)
	sessionListResult(t, d, page)

	shared := d.reopenBranchSharedCalls()
	shared.mu.Lock()
	active := len(shared.active)
	shared.mu.Unlock()
	if active != 0 {
		t.Errorf("completed page reads left %d branch inspections cached", active)
	}
}

func TestRowsOnTheSameBranchShareOneInspection(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	worktree := filepath.Join(root, "wt-shared")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/shared", worktree)
	for _, sessionID := range []string{"shared-one", "shared-two", "shared-three"} {
		writeCodexRolloutFixture(t, "conv-"+sessionID)
		closeReopenSession(t, d, reopenSession{
			ID: sessionID, Directory: worktree, Branch: "feat/shared", Repo: repo,
			Agent: "codex", Resume: "conv-" + sessionID,
		})
	}
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("delete the shared worktree directory: %v", err)
	}
	d.closeSessionReopenBroker()

	started := make(chan struct{})
	release := make(chan struct{})
	var inspections atomic.Int32
	d.reopenGitMu.Lock()
	d.reopenInspect = func(context.Context, *attngit.Client, string, string) (branchInspection, error) {
		if inspections.Add(1) == 1 {
			close(started)
		}
		<-release
		return branchInspection{State: branchStateLocal}, nil
	}
	d.reopenGitMu.Unlock()
	joined := make(chan int, 3)
	shared := d.reopenBranchSharedCalls()
	shared.mu.Lock()
	shared.joinObserver = func(_ reopenBranchKey, waiters int) { joined <- waiters }
	shared.mu.Unlock()

	type pageResult struct {
		page *protocol.SessionListResult
		err  error
	}
	done := make(chan pageResult, 1)
	go func() {
		page, err := d.sessionLedgerPage(&protocol.SessionListMessage{
			Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
		}, false)
		done <- pageResult{page: page, err: err}
	}()
	<-started
	maxWaiters := 0
	for maxWaiters < 3 {
		select {
		case waiters := <-joined:
			maxWaiters = max(maxWaiters, waiters)
		case <-time.After(5 * time.Second):
			t.Fatal("three rows did not join their shared branch inspection")
		}
	}
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.page.Reopen) != 3 {
		t.Fatalf("page returned %d verdicts, want three", len(result.page.Reopen))
	}
	if got := inspections.Load(); got != 1 {
		t.Errorf("three rows started %d branch inspections, want one in-flight call", got)
	}
}

func TestAPageReturnsACompleteBranchVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "sharpening", "feat/sharpening")

	verdict := listedVerdict(t, sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	}), "sharpening")
	if verdict.Checking {
		t.Errorf("the synchronous page returned a preliminary verdict: %+v", verdict)
	}
	if state := protocol.Deref(verdict.BranchState); state != branchStateLocal {
		t.Errorf("branch_state = %q, want %q once the check landed", state, branchStateLocal)
	}
}

func TestAClosingRowReachesTheAppBeforeItsVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	var pushed []*protocol.WebSocketEvent
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) { pushed = append(pushed, event) }
	directory := t.TempDir()
	addLedgerTestSession(t, d, "closing", directory)
	entry := protocol.SessionLedgerEntry{
		ID: "closing", Label: "closing", Agent: string(protocol.SessionAgentClaude),
		Directory: directory, WorkspaceID: "ws-closing", State: protocol.SessionStateIdle,
		LastSeen: protocol.TimestampNow().String(),
		ClosedAt: protocol.Ptr(protocol.NewTimestamp(time.Now()).String()),
		ClosedBy: protocol.Ptr(store.SessionClosedByUser),
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal the closed row: %v", err)
	}

	projectSessionClosed(d, bus.Event{Name: FactSessionClosed, Subject: "closing", Payload: payload})

	if len(pushed) != 1 {
		t.Fatalf("broadcast %d events, want one session_closed row", len(pushed))
	}
	if pushed[0].Event != protocol.EventSessionClosed || pushed[0].SessionLedgerEntry == nil {
		t.Fatalf("broadcast %+v, want the session_closed row before eligibility resolves", pushed[0])
	}
}
