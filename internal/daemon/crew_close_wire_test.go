package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func closeCrew(t *testing.T, w *world, member string) *protocol.CrewCloseResult {
	t.Helper()
	slept, err := w.Client().CrewClose(member)
	if err != nil {
		t.Fatalf("sleep %s: %v", member, err)
	}
	return slept
}

func TestCloseAsksTheLiveMemberToFileItsOwnLetter(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		day := w.bootBubbleClaude(t, string(startCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		result := closeCrew(t, w, "trellis")
		if result.AlreadyClosed || string(protocol.Deref(result.SessionID)) != day.id {
			t.Fatalf("sleep = %+v, want a request to the live member", result)
		}
		items := readInbox(t, cli, day.id, 0).Items
		if len(items) != 1 || items[0].Content != prompts.RenderText("crew", "close-requested", prompts.Values{}) {
			t.Fatalf("sleep inbox = %+v", items)
		}
		if binding := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); string(binding) != day.id {
			t.Fatalf("requesting sleep changed the member binding to %q", binding)
		}
	})
}

func TestAskingAMemberBetweenSessionsToCloseSendsNothingAndStartsNothing(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		before := crewSessionCount(t, cli)
		if slept := closeCrew(t, w, "trellis"); !slept.AlreadyClosed {
			t.Fatalf("sleep=%+v", slept)
		}
		w.advance(time.Hour)
		if got := crewSessionCount(t, cli); got != before {
			t.Fatalf("sleep woke a session: before=%d after=%d", before, got)
		}
		day := w.bootBubbleClaude(t, string(startCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		if items := readInbox(t, cli, day.id, 0).Items; len(items) != 0 {
			t.Fatalf("sleep queued mail=%+v", items)
		}
	})
}
