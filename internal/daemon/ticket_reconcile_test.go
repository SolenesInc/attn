package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

func installReconcileRunner(t *testing.T, d *Daemon) {
	t.Helper()
	runner := jobs.New(jobs.Options{
		Store:        newTestJobStore(t, d),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
	})
	if err := runner.RegisterWith(reconcileKind, d.reconcileJobHandler, jobs.HandlerConfig{
		Timeout:       ticketReconcileTimeout(),
		MaxConcurrent: ticketReconcileConcurrency,
	}); err != nil {
		t.Fatalf("register reconcile: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.jobQueue = runner
}

func reconcileTask(in ticketReconcileInputs) *jobs.Job {
	payload, err := json.Marshal(in)
	if err != nil {
		panic("marshal reconcile inputs: " + err.Error())
	}
	return &jobs.Job{
		ID:        "job-" + in.TicketID,
		Kind:      reconcileKind,
		UniqueKey: in.TicketID,
		Payload:   payload,
	}
}

func reconcileComments(t *testing.T, d *Daemon, ticketID string) []string {
	t.Helper()
	full, err := d.store.GetTicket(ticketID)
	if err != nil || full == nil {
		t.Fatalf("GetTicket %s: %v, %v", ticketID, full, err)
	}
	var out []string
	for _, a := range full.Activity {
		if a.Kind == store.TicketActivityComment && a.Author == store.TicketAuthorAttn &&
			strings.HasPrefix(a.Comment, ticketReconcileCommentPrefix) {
			out = append(out, a.Comment)
		}
	}
	return out
}

func reconciledAt(t *testing.T, d *Daemon, ticketID string) *time.Time {
	t.Helper()
	full, err := d.store.GetTicket(ticketID)
	if err != nil || full == nil {
		t.Fatalf("GetTicket %s: %v, %v", ticketID, full, err)
	}
	return full.ReconciledAt
}

func armReconcileObserver(d *Daemon, result agentdriver.HeadlessTaskResult, execErr error) (chan string, *int) {
	done := make(chan string, 8)
	calls := 0
	d.ticketReconcileDone = func(ticketID string) { done <- ticketID }
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		calls++
		return result, execErr
	}
	return done, &calls
}

func waitReconcileDone(t *testing.T, done chan string) string {
	t.Helper()
	select {
	case id := <-done:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reconciliation to finish")
		return ""
	}
}

func TestReconcileSeamNeutralEndPostsFailureNote(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	done, calls := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)
	waitReconcileDone(t, done)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status = %q, want working (no auto-transition on the orphan path)", ticket.Status)
	}
	if ticket.ReconciledAt == nil {
		t.Fatal("ReconciledAt not claimed")
	}
	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1 (%v)", len(comments), comments)
	}
	if !strings.Contains(comments[0], "could not determine") || !strings.Contains(comments[0], "could not locate") {
		t.Fatalf("failure note = %q, want could-not-determine with transcript reason", comments[0])
	}
	if *calls != 0 {
		t.Fatalf("classifier exec ran %d times, want 0 (no transcript to read)", *calls)
	}
}

func TestReconcileSeamMidFlightStampsCrashedAndClaims(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWorking)
	waitReconcileDone(t, done)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status = %q, want crashed (stamp unchanged)", ticket.Status)
	}
	if ticket.ReconciledAt == nil {
		t.Fatal("ReconciledAt not claimed")
	}
	if comments := reconcileComments(t, d, ticketID); len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
}

func TestReconcileSeamDoubleFireSingleClaim(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)
	waitReconcileDone(t, done)
	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)

	if comments := reconcileComments(t, d, ticketID); len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want exactly 1 after a double-fire", len(comments))
	}
}

func TestRunTicketReconciliationPostsVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	var seen ticketReconcileInputs
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		seen = in
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"last turn: tests pass except e2e"}`),
			TotalCostUSD:     0.12,
			NumTurns:         4,
		}, nil
	}

	if _, err := d.reconcileJobHandler(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		Title:          "Migrate the store to X",
		Brief:          "Move the store onto the new backend.",
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "codex",
		TranscriptPath: transcript,
		CloseContext:   "the session was closed (user close or teardown) while the ticket was working",
	})); err != nil {
		t.Fatalf("reconcileJobHandler: %v", err)
	}

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	for _, want := range []string{
		"Assessment: partial (confidence: medium)",
		"What's left: e2e spec never ran",
		"Evidence: last turn",
	} {
		if !strings.Contains(comments[0], want) {
			t.Fatalf("verdict comment missing %q:\n%s", want, comments[0])
		}
	}
	if seen.TranscriptPath != transcript {
		t.Fatalf("exec saw transcript %q, want %q", seen.TranscriptPath, transcript)
	}
	ticket, _ := d.store.GetTicket(ticketID)
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status = %q, want working (verdict never moves the column)", ticket.Status)
	}
}

func TestRunTicketReconciliationDropsVerdictWhenStatusMoved(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		if _, err := d.store.SetTicketStatus(ticketID, store.TicketStatusDone, store.TicketAuthorYou, "", time.Now()); err != nil {
			t.Errorf("SetTicketStatus during run: %v", err)
		}
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"high","whats_left":"x","evidence":"y"}`),
		}, nil
	}

	if _, err := d.reconcileJobHandler(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "codex",
		TranscriptPath: transcript,
	})); err != nil {
		t.Fatalf("reconcileJobHandler: %v", err)
	}

	if comments := reconcileComments(t, d, ticketID); len(comments) != 0 {
		t.Fatalf("reconcile comments = %d, want 0 (verdict dropped after status move)", len(comments))
	}
}

func TestRunTicketReconciliationExecErrorPostsFailureNote(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			Diagnostics: "headless agent MCP tool server failed",
			FailureOutput: "stderr: MCP server \"claude.ai Slack\" needs authentication\nstdout: " +
				strings.Repeat("x", 2000) + `{"type":"result","is_error":true,"result":"model not found"}`,
		}, errors.New("headless agent MCP tool server failed: exit status 1")
	}

	if _, err := d.reconcileJobHandler(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		StatusAtClaim:  store.TicketStatusWorking,
		SessionID:      sessionID,
		Agent:          "claude",
		TranscriptPath: transcript,
	})); err != nil {
		t.Fatalf("reconcileJobHandler: %v", err)
	}

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	for _, want := range []string{
		"could not determine",
		"classifier run failed: headless agent MCP tool server failed: exit status 1",
		`MCP server "claude.ai Slack" needs authentication`,
		`"result":"model not found"`,
		"…(truncated)",
	} {
		if !strings.Contains(comments[0], want) {
			t.Fatalf("failure note missing %q:\n%s", want, comments[0])
		}
	}
	if strings.Contains(comments[0], strings.Repeat("x", 1000)) {
		t.Fatalf("failure note not truncated:\n%s", comments[0])
	}
}

func TestSweepClaimsDeadOwnerAfterGrace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)
	// No session row for the assignee: the owner is dead (rows are deleted on close).
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "orphaned", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusInReview,
	}, "chief", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	t0 := time.Now()
	d.ticketReconcileSweepPass(t0)
	if got := reconciledAt(t, d, "orphaned"); got != nil {
		t.Fatalf("claimed on first sight (%v), want grace period first", got)
	}

	d.ticketReconcileSweepPass(t0.Add(ticketReconcileGrace() + time.Minute))
	waitReconcileDone(t, done)
	if got := reconciledAt(t, d, "orphaned"); got == nil {
		t.Fatal("not claimed after grace")
	}
	if comments := reconcileComments(t, d, "orphaned"); len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	ticket, _ := d.store.GetTicket("orphaned")
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("status = %q, want in_review (sweep never moves the column)", ticket.Status)
	}
}

func TestSweepSkipsLiveHumanAndUnassigned(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)

	// A session row without a backend runtime reads as live: CLI/remote sessions have
	// no daemon PTY, and their death-hook is the unregister path.
	d.store.Add(&protocol.Session{ID: "sess-live", Label: "live", Directory: t.TempDir()})
	now := time.Now()
	mk := func(id, assignee string) {
		if _, err := d.store.CreateTicket(store.Ticket{ID: id, Title: "t", Assignee: assignee, Status: store.TicketStatusWorking}, "chief", now.Add(-time.Hour)); err != nil {
			t.Fatalf("CreateTicket %s: %v", id, err)
		}
	}
	mk("live-owner", "sess-live")
	mk("human-owned", store.TicketAuthorYou)
	mk("unassigned", "")

	d.ticketReconcileSweepPass(now)
	d.ticketReconcileSweepPass(now.Add(ticketReconcileGrace() + time.Minute))

	for _, id := range []string{"live-owner", "human-owned", "unassigned"} {
		if got := reconciledAt(t, d, id); got != nil {
			t.Fatalf("%s was claimed (%v), want skipped", id, got)
		}
	}
}

func TestSweepRecoversAbandonedClaim(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	done, _ := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)
	past := time.Now().Add(-time.Hour)
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "abandoned", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusWorking,
	}, "chief", past); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// The crash gap: the seam claimed but the enqueue never landed.
	if claimed, err := d.store.ClaimTicketReconciliation("abandoned", past); err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}

	t0 := time.Now()
	d.ticketReconcileSweepPass(t0)
	if comments := reconcileComments(t, d, "abandoned"); len(comments) != 0 {
		t.Fatalf("reconciled before grace elapsed: %v", comments)
	}
	d.ticketReconcileSweepPass(t0.Add(ticketReconcileGrace() + time.Minute))
	waitReconcileDone(t, done)

	comments := reconcileComments(t, d, "abandoned")
	if len(comments) != 1 || !strings.Contains(comments[0], "could not locate") {
		t.Fatalf("recovered comments = %v, want one could-not-locate failure note", comments)
	}
}

func TestSweepSkipsTicketWithExistingTask(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	installReconcileRunner(t, d)
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "already", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusWorking,
	}, "chief", now.Add(-time.Hour)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	runner := d.jobQueueRef()
	if _, err := runner.Enqueue(reconcileKind, jobs.EnqueueOptions{
		UniqueKey: "already",
		Payload:   ticketReconcileInputs{TicketID: "already"},
	}); err != nil {
		t.Fatalf("seed reconcile job: %v", err)
	}

	d.ticketReconcileSweepPass(now.Add(ticketReconcileGrace() + time.Hour))
	if got := reconciledAt(t, d, "already"); got != nil {
		t.Fatalf("sweep re-claimed a ticket with an existing job (%v)", got)
	}
}

func TestBuildTicketReconcilePromptInlinesSlice(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"delegated: wire up the widget"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"widget wired, opening PR"}]}}`,
	}
	if err := os.WriteFile(transcriptPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture transcript: %v", err)
	}

	slice, err := transcript.ExtractConversationSlice(transcriptPath, transcript.DefaultSliceOptions())
	if err != nil {
		t.Fatalf("ExtractConversationSlice: %v", err)
	}
	if slice.Empty() {
		t.Fatalf("fixture slice unexpectedly empty")
	}

	in := ticketReconcileInputs{
		TicketID:       "tkt-1",
		Title:          "Wire up the widget",
		Brief:          "the filed ticket brief text",
		StatusAtClaim:  store.TicketStatusWorking,
		Agent:          "claude",
		TranscriptPath: transcriptPath,
		CloseContext:   "the agent process exited on its own",
	}

	prompt := buildTicketReconcilePrompt(in, slice)

	if !strings.Contains(prompt, "widget wired, opening PR") {
		t.Errorf("prompt missing inlined slice turn content:\n%s", prompt)
	}
	if !strings.Contains(prompt, "TICKET BRIEF") {
		t.Errorf("prompt missing slice heading:\n%s", prompt)
	}
	if !strings.Contains(prompt, in.Brief) {
		t.Errorf("prompt missing filed ticket brief:\n%s", prompt)
	}
	if strings.Contains(prompt, "Read the transcript") {
		t.Errorf("prompt still instructs reading the transcript:\n%s", prompt)
	}
	if strings.Contains(prompt, in.TranscriptPath) {
		t.Errorf("prompt still leaks the transcript path:\n%s", prompt)
	}
}
