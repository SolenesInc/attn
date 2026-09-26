package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTakingAnAssignedTicketNeedsConfirmationAndTellsThePreviousAssignee(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		ticketReportTake(t, cli, "z", "task-y")
		reportTicket(t, cli, "z", "task-y", protocol.DispatchWorkStateInProgress, "on it")

		if _, err := cli.TakeTicket("x", "task-y", false); err == nil || !strings.Contains(err.Error(), "--confirm") {
			t.Fatalf("taking an assigned ticket without confirming = %v, want a refusal asking for --confirm", err)
		}
		if assignee := showTicket(t, cli, "task-y").Assignee; assignee != "z" {
			t.Fatalf("the refused take reassigned the ticket to %q", assignee)
		}
		taken, err := cli.TakeTicket("x", "task-y", true)
		if err != nil || taken.TicketID != "task-y" || taken.PreviousAssignee != "z" {
			t.Fatalf("the confirmed take = %+v, %v; want task-y taken from z", taken, err)
		}
		if assignee := showTicket(t, cli, "task-y").Assignee; assignee != "x" {
			t.Errorf("after the take the ticket is assigned to %q, want x", assignee)
		}
		if told := inboxLines(t, cli, "z"); !slices.Equal(told, []string{"task-y assigned"}) {
			t.Errorf("the previous assignee was told %q, want the takeover", told)
		}
		if history := inboxLines(t, cli, "x"); !slices.Equal(history, []string{"task-y created", "task-y assigned", "task-y status_changed on it"}) {
			t.Errorf("the taker was given %q, want the ticket's history", history)
		}

		createTicket(t, cli, "planner", "Spec", "backlog-1")
		if first, err := cli.TakeTicket("x", "backlog-1", false); err != nil || first.PreviousAssignee != "" {
			t.Errorf("taking an unassigned ticket = %+v, %v; want no previous assignee", first, err)
		}
		if again, err := cli.TakeTicket("x", "backlog-1", false); err != nil || again.PreviousAssignee != "x" {
			t.Errorf("taking its own ticket again = %+v, %v; want x named as the previous assignee", again, err)
		}
		if assignee := showTicket(t, cli, "backlog-1").Assignee; assignee != "x" {
			t.Errorf("backlog-1 is assigned to %q, want x", assignee)
		}
		if _, err := cli.TakeTicket("x", "no-such-ticket", true); err == nil {
			t.Error("taking an unknown ticket succeeded")
		}
	})
}

func TestACommentReachesTheTicketsParticipantsWithoutEnlistingTheCommenter(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		ticketReportTake(t, cli, "z", "task-y")

		commented, err := cli.CommentTicket("x", "task-y", "looks good, ship it")
		if err != nil || commented.TicketID != "task-y" || !commented.Applied {
			t.Fatalf("commenting = %+v, %v", commented, err)
		}
		if _, err := cli.CommentTicket("x", "no-such-ticket", "hi"); err == nil {
			t.Error("commenting on an unknown ticket succeeded")
		}
		if told := inboxLines(t, cli, "z"); !slices.Equal(told, []string{"task-y commented looks good, ship it"}) {
			t.Errorf("the assignee was told %q, want the comment", told)
		}
		reportTicket(t, cli, "z", "task-y", protocol.DispatchWorkStateReadyForReview, "done")
		if told := inboxLines(t, cli, "x"); len(told) != 0 {
			t.Errorf("the commenter was told %q about a ticket it only commented on", told)
		}
	})
}

func TestASubscriberHearsATicketsActivityUntilItUnsubscribes(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		ticketReportTake(t, cli, "z", "task-y")

		subscribed, err := cli.SubscribeTicket("x", "task-y")
		if err != nil || subscribed.TicketID != "task-y" {
			t.Fatalf("subscribing = %+v, %v", subscribed, err)
		}
		inboxLines(t, cli, "x")
		commentOnTicket(t, cli, "reviewer", "task-y", "checking in on this thread")
		if told := inboxLines(t, cli, "x"); !slices.Equal(told, []string{"task-y commented checking in on this thread"}) {
			t.Errorf("the subscriber was told %q, want the comment", told)
		}
		if _, err := cli.UnsubscribeTicket("x", "task-y"); err != nil {
			t.Fatal(err)
		}
		commentOnTicket(t, cli, "reviewer", "task-y", "following up")
		if told := inboxLines(t, cli, "x"); len(told) != 0 {
			t.Errorf("after unsubscribing x was told %q", told)
		}

		if _, err := cli.SubscribeTicket("x", "no-such-ticket"); err == nil {
			t.Error("subscribing to an unknown ticket succeeded")
		}
		if _, err := cli.UnsubscribeTicket("x", "no-such-ticket"); err != nil {
			t.Errorf("unsubscribing from a ticket x never followed = %v, want success", err)
		}
	})
}

func TestACrewMembersTicketThreadsBelongToTheMemberAcrossItsDays(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		if err := cli.RegisterAsMember("day-a", "day-a", w.Path("day-a"), "", "trellis"); err != nil {
			t.Fatal(err)
		}
		createTicket(t, cli, "planner", "Peer", "peer-ticket")
		createTicket(t, cli, "planner", "Member work", "member-work")
		createTicket(t, cli, "planner", "Watched", "watched")

		commentOnTicket(t, cli, "day-a", "peer-ticket", "one useful note")
		if got := activityLines(showTicket(t, cli, "peer-ticket")); !slices.Equal(got, []string{"member:trellis comment one useful note"}) {
			t.Errorf("peer-ticket activity = %q, want the note attributed to the member", got)
		}
		if _, err := cli.TakeTicket("day-a", "member-work", false); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SubscribeTicket("day-a", "watched"); err != nil {
			t.Fatal(err)
		}
		inboxLines(t, cli, "day-a")
		if assignee := showTicket(t, cli, "member-work").Assignee; assignee != "day-a" {
			t.Errorf("member-work is assigned to %q, want the day session that took it", assignee)
		}

		if err := cli.Unregister("day-a"); err != nil {
			t.Fatal(err)
		}
		if err := cli.RegisterAsMember("day-b", "day-b", w.Path("day-b"), "", "trellis"); err != nil {
			t.Fatal(err)
		}
		if replayed := inboxLines(t, cli, "day-b"); len(replayed) != 0 {
			t.Errorf("the member's next day replayed %q it had already read", replayed)
		}
		for _, id := range []string{"peer-ticket", "member-work", "watched"} {
			commentOnTicket(t, cli, "reviewer", id, "new day")
		}
		reportTicket(t, cli, "observer", "watched", protocol.DispatchWorkStateReadyForReview, "finished")
		watched, err := cli.TicketInboxWatch("day-b", 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := ticketInboxBundleLines(watched); !slices.Equal(got, []string{
			"member-work commented new day", "watched commented new day", "watched status_changed finished",
		}) {
			t.Errorf("the next day watched %q, want only what is new on the threads the member took or followed", got)
		}

		if _, err := cli.UnsubscribeTicket("day-b", "watched"); err != nil {
			t.Fatal(err)
		}
		commentOnTicket(t, cli, "reviewer", "watched", "after unsubscribing")
		if told := inboxLines(t, cli, "day-b"); len(told) != 0 {
			t.Errorf("after the member unsubscribed it was told %q", told)
		}
	})
}

func TestListingTicketsLeavesUnreadActivityForTheInbox(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "worker")
		ticketReportTake(t, cli, "worker", "migration")
		commentOnTicket(t, cli, "reviewer", "migration", "one more thing to check")
		testworld.AwaitSession(app, "worker", func(s protocol.Session) bool { return protocol.Deref(s.TicketUnread) })

		if _, err := cli.TicketList("worker", "", false); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if latest := ticketLatestSession(t, app, "worker"); !protocol.Deref(latest.TicketUnread) {
			t.Fatal("listing the board cleared the worker's unread marker")
		}
		if told := inboxLines(t, cli, "worker"); !slices.Equal(told, []string{"migration commented one more thing to check"}) {
			t.Fatalf("the inbox after listing = %q, want the unread comment", told)
		}
		testworld.AwaitSession(app, "worker", func(s protocol.Session) bool { return !protocol.Deref(s.TicketUnread) })
	})
}

func TestTheSelectedCreatorOfATicketSeesItsUnreadCommentWithoutACountdown(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "creator")
		if err := cli.UpdateState("creator", protocol.StateWaitingInput); err != nil {
			t.Fatal(err)
		}
		app.Send(protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: "creator"})
		createTicket(t, cli, "creator", "Standalone", "standalone")

		commentOnTicket(t, cli, "commenter", "standalone", "please review")

		unread := testworld.AwaitSession(app, "creator", func(s protocol.Session) bool { return protocol.Deref(s.TicketUnread) })
		w.advance(0)
		if latest := ticketLatestSession(t, app, "creator"); latest.NudgeFiresAt != nil || unread.NudgeFiresAt != nil {
			t.Errorf("the selected creator has a countdown to %s", protocol.Deref(latest.NudgeFiresAt))
		}
	})
}

func ticketInboxBundleLines(inbox *protocol.TicketInboxResult) []string {
	var lines []string
	for _, bundle := range inbox.Bundles {
		for _, event := range bundle.Events {
			lines = append(lines, strings.TrimSpace(strings.Join([]string{bundle.TicketID, string(event.Kind), protocol.Deref(event.Comment)}, " ")))
		}
	}
	return lines
}

func ticketLatestSession(t *testing.T, p *testworld.Peer, id string) protocol.Session {
	t.Helper()
	updates := sessionUpdatesOf(p, id)
	if len(updates) == 0 {
		t.Fatalf("the app never heard of %s", id)
	}
	return updates[len(updates)-1]
}
