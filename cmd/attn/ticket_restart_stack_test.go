package main_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADaemonRestartKeepsTicketNudgesCountingDown(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "trellis")
	writeCharter(t, s, "alder")
	s.Start()
	app, cli := s.App(), s.Client()
	if _, err := cli.CreateTicket("planner", "watched", "", "watched"); err != nil {
		t.Fatal(err)
	}

	days := map[string]string{}
	for _, member := range []string{"trellis", "alder"} {
		woken, err := cli.CrewWake(member, "")
		if err != nil {
			t.Fatalf("wake %s: %v", member, err)
		}
		days[member] = woken.SessionID
		day := s.Launched(woken.SessionID)
		day.Prompted()
		if member == "alder" {
			day.Reply("Settled. <!-- attn:state=idle -->")
			testworld.AwaitSession(app, woken.SessionID, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
		}
		if _, err := cli.SubscribeTicket(woken.SessionID, "watched"); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.TicketInbox(woken.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cli.CommentTicket("reviewer", "watched", "while you were busy"); err != nil {
		t.Fatal(err)
	}
	for _, day := range days {
		testworld.AwaitSession(app, day, func(x protocol.Session) bool { return protocol.Deref(x.TicketUnread) && x.NudgeFiresAt != nil })
	}

	s.Stop()
	s.Start()
	app = s.App()

	for member, day := range days {
		index := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return x.ID == day })
		if index < 0 {
			t.Fatalf("%s's day %s did not survive the restart", member, day)
		}
		if resumed := app.Initial.Sessions[index]; !protocol.Deref(resumed.TicketUnread) || resumed.NudgeFiresAt == nil {
			t.Errorf("after the restart %s's day shows unread %v and a nudge at %q, want its unread ticket counting down",
				member, protocol.Deref(resumed.TicketUnread), protocol.Deref(resumed.NudgeFiresAt))
		}
	}
}
