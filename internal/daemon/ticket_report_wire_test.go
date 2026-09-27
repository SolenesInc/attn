package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAReportMovesTheReportersTicketAcrossTheBoard(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		ticketReportTake(t, cli, "worker", "migration")

		reports := []struct {
			state protocol.DispatchWorkState
			want  protocol.TicketStatus
		}{
			{protocol.DispatchWorkStateInProgress, protocol.TicketStatusWorking},
			{protocol.DispatchWorkStateNeedsInput, protocol.TicketStatusBlocked},
			{protocol.DispatchWorkStateReadyForReview, protocol.TicketStatusInReview},
			{protocol.DispatchWorkStateCompleted, protocol.TicketStatusDone},
		}
		var wantHistory []string
		for _, report := range reports {
			result, err := cli.SetTicketStatus("worker", string(report.state), "now "+string(report.state), "")
			if err != nil {
				t.Fatalf("report %s: %v", report.state, err)
			}
			if result.TicketID != "migration" || result.Status != report.want || !result.Applied {
				t.Errorf("report %s = %+v, want migration moved to %s", report.state, result, report.want)
			}
			wantHistory = append(wantHistory, "worker "+string(report.want)+" now "+string(report.state))
		}
		done := showTicket(t, cli, "migration")
		if got := ticketStatusHistory(done); !slices.Equal(got, wantHistory) {
			t.Errorf("history = %q, want %q", got, wantHistory)
		}
		if done.Status != protocol.TicketStatusDone || done.ClosedAt == nil {
			t.Errorf("after completed the ticket is %s closed at %v, want done and closed", done.Status, done.ClosedAt)
		}
		if _, err := cli.SetTicketStatus("worker", string(protocol.DispatchWorkStateInProgress), "", ""); err == nil || !strings.Contains(err.Error(), "no active ticket") {
			t.Errorf("a report after completing = %v, want no active ticket", err)
		}

		ticketReportTake(t, cli, "worker", "migration-retry")
		failed, err := cli.SetTicketStatus("worker", string(protocol.DispatchWorkStateFailed), "gave up", "")
		if err != nil || failed.Status != protocol.TicketStatusFailed {
			t.Fatalf("report failed = %+v, %v", failed, err)
		}
		if closed := showTicket(t, cli, "migration-retry"); closed.ClosedAt == nil {
			t.Error("a failed ticket is not closed")
		}
	})
}

func TestAReportBehindUnreadCommentsCatchesUpInsteadOfMoving(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		ticketReportTake(t, cli, "worker", "migration")
		reportTicket(t, cli, "worker", "migration", protocol.DispatchWorkStateInProgress, "")
		commentOnTicket(t, cli, "reviewer", "migration", "read this first")

		held, err := cli.SetTicketStatus("worker", string(protocol.DispatchWorkStateReadyForReview), "should not land", "")
		if err != nil {
			t.Fatal(err)
		}
		if held.Applied || held.Status != protocol.TicketStatusWorking || held.CatchUp == nil ||
			len(held.CatchUp.Events) != 1 || protocol.Deref(held.CatchUp.Events[0].Comment) != "read this first" {
			t.Fatalf("the report behind a comment = %+v, want the comment as catch-up and the ticket still working", held)
		}
		if status := showTicket(t, cli, "migration").Status; status != protocol.TicketStatusWorking {
			t.Fatalf("the held report moved the ticket to %s", status)
		}
		retry, err := cli.SetTicketStatus("worker", string(protocol.DispatchWorkStateReadyForReview), "now reviewed", "")
		if err != nil || !retry.Applied || retry.CatchUp != nil || retry.Status != protocol.TicketStatusInReview {
			t.Fatalf("the retry = %+v, %v; want it applied", retry, err)
		}
	})
}

func TestAnySessionMovesATicketByIDAndIsRecordedAsTheAuthor(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		createTicket(t, cli, "someone-else", "Migrate the store", "store-migration")

		result, err := cli.SetTicketStatus("observer", string(protocol.DispatchWorkStateReadyForReview), "moving it along", "store-migration")
		if err != nil {
			t.Fatal(err)
		}
		if result.TicketID != "store-migration" || result.Status != protocol.TicketStatusInReview || !result.Applied {
			t.Fatalf("moving by id = %+v", result)
		}
		if got := ticketStatusHistory(showTicket(t, cli, "store-migration")); !slices.Equal(got, []string{"observer in_review moving it along"}) {
			t.Errorf("history = %q, want the move by observer", got)
		}
	})
}

func TestTicketReportRefusalsNameWhatIsWrong(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		ticketReportTake(t, cli, "worker", "migration")

		for _, refusal := range []struct {
			name, session, state, ticket, want string
		}{
			{"no session", "", string(protocol.DispatchWorkStateInProgress), "", "source_session_id is required"},
			{"unknown work state", "worker", "marshmallow", "", "unknown work state"},
			{"no ticket of its own", "ghost-session", string(protocol.DispatchWorkStateInProgress), "", "no active ticket"},
			{"unknown ticket", "observer", string(protocol.DispatchWorkStateInProgress), "does-not-exist", "does-not-exist"},
		} {
			if _, err := cli.SetTicketStatus(refusal.session, refusal.state, "", refusal.ticket); err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Errorf("%s: report = %v, want a refusal naming %q", refusal.name, err, refusal.want)
			}
		}
	})
}

func ticketReportTake(t *testing.T, cli *client.Client, session, id string) {
	t.Helper()
	createTicket(t, cli, "planner", id, id)
	if _, err := cli.TakeTicket(session, id, false); err != nil {
		t.Fatalf("%s takes %s: %v", session, id, err)
	}
	inboxLines(t, cli, session)
}

func ticketStatusHistory(ticket *protocol.Ticket) []string {
	var history []string
	for _, a := range ticket.Activity {
		if a.Kind == protocol.TicketActivityKindStatusChange {
			history = append(history, strings.Join([]string{a.Author, string(protocol.Deref(a.ToStatus)), protocol.Deref(a.Comment)}, " "))
		}
	}
	return history
}
