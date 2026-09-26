package daemon_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARespawnedSessionRevivesItsCrashedTickets(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	worker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(worker)
	ticketSessionEndAtWork(t, app, agent, worker, "keep going")
	for _, id := range []string{"checkout", "invoices", "shipped"} {
		createTicket(t, cli, "planner", id, id)
		if _, err := cli.TakeTicket(worker, id, false); err != nil {
			t.Fatal(err)
		}
	}
	inboxLines(t, cli, worker)
	reportTicket(t, cli, worker, "shipped", protocol.DispatchWorkStateCompleted, "done")
	agent.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == worker })
	commentOnTicket(t, cli, "reviewer", "checkout", "scope changed while you were down")

	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ID = worker })
	revived := w.Launched(worker)

	for id, want := range map[string]protocol.TicketStatus{
		"checkout": protocol.TicketStatusWorking,
		"invoices": protocol.TicketStatusWorking,
		"shipped":  protocol.TicketStatusDone,
	} {
		ticket := showTicket(t, cli, id)
		if ticket.Status != want || (want == protocol.TicketStatusWorking) != (ticket.ClosedAt == nil) {
			t.Errorf("%s after the respawn is %s closed at %v, want %s", id, ticket.Status, protocol.Deref(ticket.ClosedAt), want)
		}
	}
	if got := ticketStatusHistory(showTicket(t, cli, "invoices")); !slices.Equal(got, []string{
		"attn crashed agent process ended mid-flight without reporting", "attn working session was reloaded and is running again",
	}) {
		t.Errorf("invoices history = %q, want the crash and the revival by attn", got)
	}

	held, err := cli.SetTicketStatus(worker, string(protocol.DispatchWorkStateReadyForReview), "should not land", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if held.Applied || held.CatchUp == nil || len(held.CatchUp.Events) != 3 {
		t.Fatalf("the first report on checkout = %+v, want it held with the crash, the comment and the revival", held)
	}
	reportTicket(t, cli, worker, "checkout", protocol.DispatchWorkStateReadyForReview, "now reviewed")
	first, err := cli.SetTicketStatus(worker, string(protocol.DispatchWorkStateInProgress), "back at it", "invoices")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Applied || first.Status != protocol.TicketStatusWorking || first.CatchUp == nil || len(first.CatchUp.Events) != 2 {
		t.Fatalf("the first report on invoices = %+v, want it applied with the crash and revival beside it", first)
	}

	ticketSessionEndAtWork(t, app, revived, worker, "and again")
	revived.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == worker })
	if status := showTicket(t, cli, "invoices").Status; status != protocol.TicketStatusCrashed {
		t.Errorf("invoices after the revived session died mid-flight = %s, want crashed again", status)
	}
}

func TestASessionThatEndsByTheUsersHandOrAtRestLeavesItsTicketsWhereTheyWere(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	spawned, workspaceID, paneID := w.RequestSpawn(app, fakeagent.Claude, w.Path("closed"))
	closed := spawned.ID
	ticketSessionEndAtWork(t, app, w.Launched(closed), closed, "open the pull request")
	ticketReportTake(t, cli, closed, "reviewed")
	reportTicket(t, cli, closed, "reviewed", protocol.DispatchWorkStateReadyForReview, "PR is up")
	ticketReportTake(t, cli, closed, "in-flight")
	reportTicket(t, cli, closed, "in-flight", protocol.DispatchWorkStateInProgress, "")

	resting := w.Spawn(app, fakeagent.Claude, w.Path("resting"))
	agent := w.Launched(resting)
	ticketSessionEndAtWork(t, app, agent, resting, "wrap up")
	ticketReportTake(t, cli, resting, "at-rest")
	reportTicket(t, cli, resting, "at-rest", protocol.DispatchWorkStateInProgress, "")
	agent.Reply("Done for now. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, resting, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	testworld.Request(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	}, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
		return r.Action == protocol.CmdWorkspaceLayoutClosePane && protocol.Deref(r.PaneID) == paneID
	})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == closed })
	agent.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == resting })

	for id, want := range map[string]protocol.TicketStatus{
		"reviewed":  protocol.TicketStatusInReview,
		"in-flight": protocol.TicketStatusWorking,
		"at-rest":   protocol.TicketStatusWorking,
	} {
		if got := showTicket(t, cli, id).Status; got != want {
			t.Errorf("%s = %s, want %s", id, got, want)
		}
	}
}

func TestATicketWhoseOwnerEndsGetsOneReconciliationNoteAndKeepsItsColumn(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		t.Setenv("ATTN_HEADLESS_TASKS", "on")
		w.finishStartupWork()
		cli := w.Client()
		owners := map[string]string{"resting-owner": protocol.StateWaitingInput, "working-owner": protocol.StateWorking}
		for owner, state := range owners {
			registerSessions(t, w, cli, owner)
			ticketReportTake(t, cli, owner, owner+"-ticket")
			reportTicket(t, cli, owner, owner+"-ticket", protocol.DispatchWorkStateInProgress, "")
			if err := cli.UpdateState(owner, state); err != nil {
				t.Fatal(err)
			}
		}

		for owner := range owners {
			if err := cli.Unregister(owner); err != nil {
				t.Fatal(err)
			}
			w.advance(0)
		}

		for owner := range owners {
			ticket := showTicket(t, cli, owner+"-ticket")
			notes := ticketReconciliationNotes(ticket)
			if len(notes) != 1 || !strings.Contains(notes[0], "could not determine") || !strings.Contains(notes[0], "could not locate") ||
				!strings.Contains(notes[0], "the session was closed (user close or teardown)") {
				t.Errorf("%s's reconciliation notes = %q, want one saying the closed session's outcome could not be determined without a transcript", owner, notes)
			}
			if ticket.Status != protocol.TicketStatusWorking {
				t.Errorf("%s's ticket moved to %s, want it left working", owner, ticket.Status)
			}
		}
	})
}

func TestAnOrphanedTicketIsReconciledOnlyAfterItsGrace(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		t.Setenv("ATTN_HEADLESS_TASKS", "on")
		w.finishStartupWork()
		cli := w.Client()
		registerSessions(t, w, cli, "live-owner")
		for owner, id := range map[string]string{"gone-owner": "orphan", "live-owner": "live", "you": "human"} {
			ticketReportTake(t, cli, owner, id)
			reportTicket(t, cli, owner, id, protocol.DispatchWorkStateInProgress, "")
		}
		createTicket(t, cli, "planner", "Nobody's", "unassigned")

		w.advance(15 * time.Minute)
		for _, id := range []string{"orphan", "live", "human", "unassigned"} {
			if notes := ticketReconciliationNotes(showTicket(t, cli, id)); len(notes) != 0 {
				t.Fatalf("%s was reconciled %q before the orphan grace ran out", id, notes)
			}
		}
		w.advance(5 * time.Minute)
		orphan := showTicket(t, cli, "orphan")
		if notes := ticketReconciliationNotes(orphan); len(notes) != 1 || !strings.Contains(notes[0], "found orphaned by the periodic sweep") {
			t.Fatalf("the orphan's reconciliation notes = %q, want one from the sweep", notes)
		}
		if orphan.Status != protocol.TicketStatusWorking {
			t.Errorf("the sweep moved the orphan to %s", orphan.Status)
		}
		w.advance(5 * time.Minute)
		for id, want := range map[string]int{"orphan": 1, "live": 0, "human": 0, "unassigned": 0} {
			if notes := ticketReconciliationNotes(showTicket(t, cli, id)); len(notes) != want {
				t.Errorf("%s has reconciliation notes %q, want %d", id, notes, want)
			}
		}
	})
}

func ticketSessionEndAtWork(t *testing.T, app *testworld.Peer, agent *fakeagent.Run, session, prompt string) {
	t.Helper()
	app.TypeLine(session, prompt)
	agent.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func ticketReconciliationNotes(ticket *protocol.Ticket) []string {
	var notes []string
	for _, a := range ticket.Activity {
		if a.Kind == protocol.TicketActivityKindComment && a.Author == "attn" {
			notes = append(notes, protocol.Deref(a.Comment))
		}
	}
	return notes
}
