package daemon_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const ticketNudgeCountdown = 30 * time.Second

const ticketNudgeBundleWindow = 10 * time.Minute

var ticketNudgePromptText = prompts.RenderText("session", "legacy-ticket-nudge", prompts.Values{})

func TestTicketNudgesComeAtOnceAfterQuietAndBundleABurst(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "worker", "reviewer")
		createTicket(t, cli, "planner", "Price the order", "pricing")
		if _, err := cli.TakeTicket("worker", "pricing", false); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SubscribeTicket("reviewer", "pricing"); err != nil {
			t.Fatal(err)
		}

		commentOnTicket(t, cli, "chief", "pricing", "first")
		for _, session := range []string{"worker", "reviewer"} {
			ticketNudgeAwaitDeadline(t, w, app, session, time.Now().Add(ticketNudgeCountdown))
		}
		w.advance(ticketNudgeCountdown)
		for _, session := range []string{"worker", "reviewer"} {
			ticketNudgeAwaitDelivered(t, w, app, cli, session)
		}
		for _, state := range []string{protocol.StateWorking, protocol.StateWaitingInput} {
			if err := cli.UpdateState("reviewer", state); err != nil {
				t.Fatal(err)
			}
		}
		w.advance(0)
		if rearmed := ticketLatestSession(t, app, "reviewer").NudgeFiresAt; rearmed != nil {
			t.Fatalf("activity already nudged about rearmed the reviewer's countdown to %s", *rearmed)
		}
		if got := inboxLines(t, cli, "worker"); !slices.Equal(got, []string{"pricing created", "pricing commented first"}) {
			t.Fatalf("the worker read %q", got)
		}

		burstEnds := time.Now().Add(ticketNudgeBundleWindow)
		commentOnTicket(t, cli, "chief", "pricing", "second")
		for _, session := range []string{"worker", "reviewer"} {
			ticketNudgeAwaitDeadline(t, w, app, session, burstEnds)
		}
		w.advance(4 * time.Minute)
		commentOnTicket(t, cli, "chief", "pricing", "third")
		reportTicket(t, cli, "observer", "pricing", protocol.DispatchWorkStateCompleted, "shipped")
		w.advance(0)
		for _, session := range []string{"worker", "reviewer"} {
			if deadline := ticketLatestSession(t, app, session).NudgeFiresAt; protocol.Deref(deadline) != ticketNudgeStamp(burstEnds) {
				t.Errorf("inside the burst %s's nudge moved to %s, want it held at %s", session, protocol.Deref(deadline), ticketNudgeStamp(burstEnds))
			}
		}
		w.advance(time.Until(burstEnds) - time.Second)
		for _, session := range []string{"worker", "reviewer"} {
			if early := readInbox(t, cli, session, 0); len(early.Items) != 0 {
				t.Errorf("%s was nudged %q before the burst's bundle deadline", session, inboxContents(early.Items))
			}
		}
		w.advance(time.Second)
		for _, session := range []string{"worker", "reviewer"} {
			ticketNudgeAwaitDelivered(t, w, app, cli, session)
		}
		if got := inboxLines(t, cli, "worker"); !slices.Equal(got, []string{"pricing commented second", "pricing commented third", "pricing status_changed shipped"}) {
			t.Fatalf("the bundle carried %q, want the whole burst", got)
		}

		w.advance(ticketNudgeBundleWindow)
		commentOnTicket(t, cli, "chief", "pricing", "after quiet")
		ticketNudgeAwaitDeadline(t, w, app, "worker", time.Now().Add(ticketNudgeCountdown))
		w.advance(ticketNudgeCountdown)
		ticketNudgeAwaitDelivered(t, w, app, cli, "worker")
	})
}

func TestReadingTheTicketInboxInsideABundleWindowDeliversAtOnceAndDisarmsTheNudge(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "worker")
		ticketReportTake(t, cli, "worker", "pricing")

		reads := []struct {
			mode string
			read func(string) (*protocol.TicketInboxResult, error)
		}{
			{"watch", func(id string) (*protocol.TicketInboxResult, error) { return cli.TicketInboxWatch(id, 0) }},
			{"explicit", cli.TicketInbox},
		}
		for _, read := range reads {
			commentOnTicket(t, cli, "chief", "pricing", "deliver through "+read.mode)
			ticketNudgeAwaitDeadline(t, w, app, "worker", time.Now().Add(ticketNudgeBundleWindow))
			inbox, err := read.read("worker")
			if err != nil {
				t.Fatal(err)
			}
			if got := ticketInboxBundleLines(inbox); !slices.Equal(got, []string{"pricing commented deliver through " + read.mode}) {
				t.Errorf("a %s read inside the bundle window returned %q, want the comment at once", read.mode, got)
			}
			testworld.AwaitSession(app, "worker", func(s protocol.Session) bool { return s.NudgeFiresAt == nil && !protocol.Deref(s.TicketUnread) })
		}
		w.advance(ticketNudgeBundleWindow)
		if late := readInbox(t, cli, "worker", 0); len(late.Items) != 0 {
			t.Errorf("activity read in time was still nudged: %q", inboxContents(late.Items))
		}
	})
}

func TestTicketActivityNudgesAParticipantOnceItsCountdownRunsOut(t *testing.T) {
	for _, agent := range []protocol.SessionAgent{protocol.SessionAgentCodex, protocol.SessionAgentClaude} {
		for _, state := range []string{protocol.StateWaitingInput, protocol.StateWorking} {
			t.Run(string(agent)+"/"+state, func(t *testing.T) {
				inBubble(t, func(t *testing.T, w *world) {
					w.finishStartupWork()
					app, cli := w.App(), w.Client()
					if err := cli.RegisterWithAgent("worker", "worker", w.Path("worker"), string(agent)); err != nil {
						t.Fatal(err)
					}
					ticketReportTake(t, cli, "worker", "pricing")
					w.advance(ticketNudgeBundleWindow)
					if err := cli.UpdateState("worker", state); err != nil {
						t.Fatal(err)
					}
					testworld.AwaitSession(app, "worker", func(s protocol.Session) bool { return string(s.State) == state })

					commentOnTicket(t, cli, "chief", "pricing", "take a look at the failing test")
					ticketNudgeAwaitDeadline(t, w, app, "worker", time.Now().Add(ticketNudgeCountdown))
					w.advance(ticketNudgeCountdown)
					ticketNudgeAwaitDelivered(t, w, app, cli, "worker")
					if got := inboxLines(t, cli, "worker"); !slices.Equal(got, []string{"pricing commented take a look at the failing test"}) {
						t.Errorf("the inbox returned %q", got)
					}
					if again := inboxLines(t, cli, "worker"); len(again) != 0 {
						t.Errorf("a second read returned %q again", again)
					}
				})
			})
		}
	}
}

func TestAParticipantAwaitingApprovalIsNudgedOnlyOnceItMovesOn(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "worker")
		ticketReportTake(t, cli, "worker", "pricing")
		w.advance(ticketNudgeBundleWindow)
		if err := cli.UpdateState("worker", protocol.StatePendingApproval); err != nil {
			t.Fatal(err)
		}

		commentOnTicket(t, cli, "chief", "pricing", "take a look")
		testworld.AwaitSession(app, "worker", func(s protocol.Session) bool { return protocol.Deref(s.TicketUnread) })
		w.advance(ticketNudgeCountdown)
		if latest := ticketLatestSession(t, app, "worker"); latest.NudgeFiresAt != nil {
			t.Errorf("a session awaiting approval armed a countdown to %s", *latest.NudgeFiresAt)
		}
		if early := readInbox(t, cli, "worker", 0); len(early.Items) != 0 {
			t.Fatalf("a session awaiting approval was nudged %q", inboxContents(early.Items))
		}

		if err := cli.UpdateState("worker", protocol.StateWorking); err != nil {
			t.Fatal(err)
		}
		ticketNudgeAwaitDeadline(t, w, app, "worker", time.Now().Add(ticketNudgeCountdown))
		w.advance(ticketNudgeCountdown)
		ticketNudgeAwaitDelivered(t, w, app, cli, "worker")
	})
}

func TestALiveWatcherReceivesActivityInsteadOfANudge(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "worker")
		ticketReportTake(t, cli, "worker", "pricing")
		w.advance(ticketNudgeBundleWindow)
		if first, err := cli.TicketInboxWatch("worker", 30*time.Second); err != nil || len(first.Bundles) != 0 {
			t.Fatalf("the first watch poll = %+v, %v; want nothing new", first, err)
		}
		polled := time.Now()

		commentOnTicket(t, cli, "chief", "pricing", "take a look")
		ticketNudgeAwaitDeadline(t, w, app, "worker", polled.Add(ticketNudgeCountdown))
		w.advance(ticketNudgeCountdown)
		ticketNudgeAwaitDeadline(t, w, app, "worker", polled.Add(45*time.Second))
		w.advance(14 * time.Second)
		if doorbelled := readInbox(t, cli, "worker", 0); len(doorbelled.Items) != 0 {
			t.Fatalf("a watcher polling every 30s was nudged %q between polls", inboxContents(doorbelled.Items))
		}

		watched, err := cli.TicketInboxWatch("worker", 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if got := ticketInboxBundleLines(watched); !slices.Equal(got, []string{"pricing commented take a look"}) {
			t.Fatalf("the next watch poll returned %q, want the comment", got)
		}
		testworld.AwaitSession(app, "worker", func(s protocol.Session) bool { return s.NudgeFiresAt == nil })
		if again, err := cli.TicketInboxWatch("worker", 30*time.Second); err != nil || len(again.Bundles) != 0 {
			t.Errorf("the poll after = %+v, %v; want the comment consumed once", again, err)
		}
		w.advance(2 * time.Second)
		if late := readInbox(t, cli, "worker", 0); len(late.Items) != 0 {
			t.Errorf("activity the watcher read was still nudged: %q", inboxContents(late.Items))
		}
	})
}

func TestNeitherSiblingsNorASessionsOwnTicketsNudgeIt(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		siblings := []string{"a", "b", "c"}
		ticketNudgeReadySessions(t, w, cli, siblings...)
		for _, sibling := range siblings {
			ticketReportTake(t, cli, sibling, "task-"+sibling)
		}

		reportTicket(t, cli, "c", "task-c", protocol.DispatchWorkStateCompleted, "done")
		createTicket(t, cli, "a", "Own work", "own-work")
		if _, err := cli.TakeTicket("a", "own-work", false); err != nil {
			t.Fatal(err)
		}
		w.advance(ticketNudgeCountdown)

		for _, sibling := range siblings {
			latest := ticketLatestSession(t, app, sibling)
			if protocol.Deref(latest.TicketUnread) || latest.NudgeFiresAt != nil {
				t.Errorf("%s shows unread %v with a countdown to %s", sibling, protocol.Deref(latest.TicketUnread), protocol.Deref(latest.NudgeFiresAt))
			}
			if nudged := readInbox(t, cli, sibling, 0); len(nudged.Items) != 0 {
				t.Errorf("%s was nudged %q", sibling, inboxContents(nudged.Items))
			}
		}
	})
}

func TestAWakeRefusedByTheLimitIsVisibleAndLeavesTheMemberUnread(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		writeCrewCharter(t, w, "alder")
		w.restart()
		app, cli := w.App(), w.Client()
		createTicket(t, cli, "planner", "Refused thread", "refused-thread")
		if err := cli.RegisterAsMember("alder-day", "alder-day", w.Path("alder-day"), "", "alder"); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SubscribeTicket("alder-day", "refused-thread"); err != nil {
			t.Fatal(err)
		}
		inboxLines(t, cli, "alder-day")
		if err := cli.Unregister("alder-day"); err != nil {
			t.Fatal(err)
		}
		setSetting(t, app, "crew.wake_limit", "0")
		sessions := crewSessionCount(t, cli)

		commentOnTicket(t, cli, "chief", "refused-thread", "wake up")
		testworld.Await(app, protocol.EventNotificationsUpdated, func(m protocol.NotificationsUpdatedMessage) bool { return m.UnreadCount == 1 })

		if after := crewSessionCount(t, cli); after != sessions {
			t.Errorf("the refused wake left %d sessions, want %d", after, sessions)
		}
		if bound := crewRosterMember(t, cli, "alder").BindingSession; bound != nil {
			t.Errorf("alder is bound to %s after a refused wake", *bound)
		}
		feed := listNotifications(app).Notifications
		if len(feed) != 1 || feed[0].Kind != "crew_ticket_wake_refused" || feed[0].SourceID != "refused-thread" ||
			!strings.Contains(feed[0].Detail, "crew.wake_limit=0") || !strings.Contains(feed[0].Body, "still unread") {
			t.Fatalf("notifications = %+v, want one naming the wake limit and the unread ticket", feed)
		}
		if err := cli.RegisterAsMember("alder-later", "alder-later", w.Path("alder-later"), "", "alder"); err != nil {
			t.Fatal(err)
		}
		if got := inboxLines(t, cli, "alder-later"); !slices.Equal(got, []string{"refused-thread commented wake up"}) {
			t.Errorf("alder's next day read %q, want the comment still unread", got)
		}
	})
}

func ticketNudgeReadySessions(t *testing.T, w *world, cli *client.Client, ids ...string) {
	t.Helper()
	registerSessions(t, w, cli, ids...)
	for _, id := range ids {
		if err := cli.UpdateState(id, protocol.StateWaitingInput); err != nil {
			t.Fatal(err)
		}
	}
}

func ticketNudgeStamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339Nano)
}

func ticketNudgeAwaitDeadline(t *testing.T, w *world, app *testworld.Peer, session string, deadline time.Time) {
	t.Helper()
	w.advance(0)
	if got := protocol.Deref(ticketLatestSession(t, app, session).NudgeFiresAt); got != ticketNudgeStamp(deadline) {
		t.Fatalf("%s's nudge fires at %q, want %q", session, got, ticketNudgeStamp(deadline))
	}
}

func ticketNudgeAwaitDelivered(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, session string) {
	t.Helper()
	w.advance(0)
	if latest := ticketLatestSession(t, app, session); latest.NudgeFiresAt != nil {
		t.Fatalf("%s still counts down to %s", session, *latest.NudgeFiresAt)
	}
	if nudged := readInbox(t, cli, session, 0); len(nudged.Items) != 1 || nudged.Items[0].Content != ticketNudgePromptText {
		t.Fatalf("%s was nudged %q, want the ticket nudge once", session, inboxContents(nudged.Items))
	}
}
