package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func respawnDelegatedSession(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("session %s missing before respawn", sessionID)
	}
	client := newWorkspaceProtocolTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         session.Directory,
		WorkspaceID: session.WorkspaceID,
		Agent:       string(session.Agent),
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)
}

func TestRespawnOfCrashedSessionRevivesTicketToWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after crash: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after crash = %q, want crashed", ticket.Status)
	}

	respawnDelegatedSession(t, d, sessionID)

	ticket, err = d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after respawn: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status after respawn = %q, want working", ticket.Status)
	}
	if ticket.ClosedAt != nil {
		t.Fatal("revived ticket still carries closed_at")
	}

	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	var revive *store.TicketEvent
	for i := range events {
		if events[i].TicketID == ticketID &&
			events[i].FromStatus == store.TicketStatusCrashed &&
			events[i].ToStatus == store.TicketStatusWorking {
			revive = &events[i]
		}
	}
	if revive == nil {
		t.Fatalf("no crashed->working event for ticket %q", ticketID)
	}
	if revive.Author != store.TicketAuthorAttn {
		t.Fatalf("revive author = %q, want attn", revive.Author)
	}

	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
	ticket, err = d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after second crash: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after second crash = %q, want crashed (detection re-armed)", ticket.Status)
	}
}

func TestRespawnLeavesNonCrashedTicketAlone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateReadyForReview), "PR is up")

	d.store.UpdateState(sessionID, protocol.StateWaitingInput)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 0})
	respawnDelegatedSession(t, d, sessionID)

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("status = %q, want in_review (revival must not touch other columns)", ticket.Status)
	}
}

func TestRevivedSessionReportsOnFirstAttempt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
	respawnDelegatedSession(t, d, sessionID)

	resp := callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateReadyForReview), "PR is up")
	result := resp.TicketStatusResult
	if !resp.Ok || result == nil || !result.Applied {
		t.Fatalf("first report after revive = %+v, want applied", resp)
	}
	if result.Status != protocol.TicketStatusInReview {
		t.Fatalf("reported status = %q, want in_review", result.Status)
	}
	if result.CatchUp == nil || len(result.CatchUp.Events) != 2 {
		t.Fatalf("catch-up = %+v, want the crash and revive records delivered beside the report", result.CatchUp)
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil || ticket.Status != store.TicketStatusInReview {
		t.Fatalf("ticket after report = %+v (err %v)", ticket, err)
	}
}

func TestRevivedSessionStillGatedByPeerActivity(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)

	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
	if _, err := d.store.AddTicketComment(ticketID, "chief-peer", "scope changed while you were down", time.Now()); err != nil {
		t.Fatal(err)
	}
	respawnDelegatedSession(t, d, sessionID)

	first := callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateReadyForReview), "should not land")
	result := first.TicketStatusResult
	if !first.Ok || result == nil || result.Applied {
		t.Fatalf("first report = %+v, want refused", first)
	}
	if result.CatchUp == nil || len(result.CatchUp.Events) != 3 {
		t.Fatalf("catch-up = %+v, want crash, peer comment and revive shown", result.CatchUp)
	}

	retry := callSetTicketStatus(t, d, sessionID, string(protocol.DispatchWorkStateReadyForReview), "now reviewed")
	if !retry.Ok || retry.TicketStatusResult == nil || !retry.TicketStatusResult.Applied ||
		retry.TicketStatusResult.Status != protocol.TicketStatusInReview {
		t.Fatalf("retry = %+v", retry)
	}
}

func TestRecoveryAdoptRevivesCrashedTicketToWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	session := d.store.Get(sessionID)

	d.store.UpdateState(sessionID, protocol.StateWorking)
	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateWorking)
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after crash: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("status after crash = %q, want crashed", ticket.Status)
	}

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{sessionID},
		info: map[string]ptybackend.SessionInfo{
			sessionID: {
				SessionID: sessionID,
				Agent:     string(session.Agent),
				CWD:       session.Directory,
				Running:   true,
				State:     protocol.StateWorking,
			},
		},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	ticket, err = d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket after adopt: %v, %v", ticket, err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("status after adopt = %q, want working", ticket.Status)
	}
}

func TestReviveRestoresEveryCrashedTicketOfTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	now := time.Now()
	for _, id := range []string{"tk-a", "tk-b", "tk-c"} {
		if _, err := d.store.CreateTicket(store.Ticket{
			ID: id, Title: id, Status: store.TicketStatusWorking, Assignee: "sess-1",
		}, "chief-1", now); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if _, err := d.store.SetTicketStatus(id, store.TicketStatusCrashed, store.TicketAuthorAttn, "died", now); err != nil {
			t.Fatalf("crash %s: %v", id, err)
		}
	}

	d.reviveCrashedTicketsForSession("sess-1")

	for _, id := range []string{"tk-a", "tk-b", "tk-c"} {
		ticket, err := d.store.GetTicket(id)
		if err != nil || ticket == nil {
			t.Fatalf("GetTicket %s: %v, %v", id, ticket, err)
		}
		if ticket.Status != store.TicketStatusWorking {
			t.Fatalf("%s status = %q, want working", id, ticket.Status)
		}
	}
}
