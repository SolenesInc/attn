package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
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
	sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	})
	waitForBranchInspection(t, d, repo, "feat/agreed")

	listed := listedVerdict(t, sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	}), "agreed")
	waitForBranchInspection(t, d, repo, "feat/agreed")
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
	d.branchInspectionsMu.Lock()
	inspections := len(d.branchInspections) + len(d.branchInspectionsRunning)
	d.branchInspectionsMu.Unlock()
	if inspections != 0 {
		t.Errorf("an unasked page started %d branch inspections, want none", inspections)
	}
}

func TestReadingAPageTwiceInspectsNothingTheSecondTime(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "twice", "feat/twice")

	page := protocol.SessionListMessage{Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true)}
	sessionListResult(t, d, page)
	waitForBranchInspection(t, d, repo, "feat/twice")

	sessionListResult(t, d, page)

	d.branchInspectionsMu.Lock()
	running := len(d.branchInspectionsRunning)
	d.branchInspectionsMu.Unlock()
	if running != 0 {
		t.Errorf("reading the page again started %d inspections, want the stored answer reused", running)
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

	sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	})
	waitForBranchInspection(t, d, repo, "feat/shared")

	d.branchInspectionsMu.Lock()
	keys := len(d.branchInspections)
	d.branchInspectionsMu.Unlock()
	if keys != 1 {
		t.Errorf("three rows on one branch left %d inspections behind, want the one they share", keys)
	}
}

func TestAPageAnswersBeforeTheBranchCheckAndSharpensAfterIt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo, _, root := newReopenRepo(t)
	closeWorktreeRow(t, d, repo, root, "sharpening", "feat/sharpening")

	first := listedVerdict(t, sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	}), "sharpening")
	if !first.Checking {
		t.Errorf("the first page said checking=false before any inspection ran: %+v", first)
	}
	waitForBranchInspection(t, d, repo, "feat/sharpening")

	second := listedVerdict(t, sessionListResult(t, d, protocol.SessionListMessage{
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	}), "sharpening")
	if second.Checking {
		t.Errorf("the page is still checking after its inspection landed: %+v", second)
	}
	if state := protocol.Deref(second.BranchState); state != branchStateLocal {
		t.Errorf("branch_state = %q, want %q once the check landed", state, branchStateLocal)
	}
}

func TestAClosingRowReachesTheAppAlreadyJudged(t *testing.T) {
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

	if len(pushed) != 1 || pushed[0].Reopen == nil {
		t.Fatalf("broadcast %d events, want one session_closed carrying a verdict", len(pushed))
	}
	if pushed[0].Reopen.DirectoryState != directoryPresent {
		t.Errorf("the row reached the app judged %s, want %s: its directory is still there",
			pushed[0].Reopen.DirectoryState, directoryPresent)
	}
	if len(pushed[0].Reopen.Actions) == 0 {
		t.Errorf("the row reached the app with no way back: %+v", pushed[0].Reopen)
	}
}
