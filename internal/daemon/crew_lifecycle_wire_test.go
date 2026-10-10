package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var crewHeartbeat = prompts.RenderText("crew", "heartbeat", prompts.Values{})

func crewDayInBubble(t *testing.T, w *world, settings map[string]string) (*testworld.Peer, *bubbleClaude) {
	t.Helper()
	writeCrewCharter(t, w, "trellis")
	w.restart()
	app, cli := w.App(), w.Client()
	for key, value := range settings {
		setSetting(t, app, key, value)
	}
	day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
	day.reply("Ready. <!-- attn:state=idle -->")
	return app, day
}

func pastedContaining(term *testworld.Terminal, text string) int {
	count := 0
	for _, pasted := range term.Pasted() {
		if strings.Contains(pasted, text) {
			count++
		}
	}
	return count
}

func TestACrewDayWhoseContextIsAboutToLapseIsWarmedOnce(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		_, day := crewDayInBubble(t, w, nil)

		w.advance(54 * time.Minute)
		if got := day.term.Pasted(); len(got) != 0 {
			t.Fatalf("an attended day with a fresh context was sent %q", got)
		}
		w.advance(2 * time.Minute)
		if got := pastedContaining(day.term, crewHeartbeat); got != 1 {
			t.Fatalf("a context five minutes from lapsing was sent %q, want one heartbeat", day.term.Pasted())
		}
		day.reply("Still here. <!-- attn:state=idle -->")
		w.advance(50 * time.Minute)
		if got := len(day.term.Pasted()); got != 1 {
			t.Fatalf("the warmed context was nudged again: %q", day.term.Pasted())
		}
	})
}

func TestACrewDayWaitingOnTheUserIsWarmedLikeAnIdleOne(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, day := crewDayInBubble(t, w, nil)
		app.TypeLine(day.id, "pick a colour for the banner")
		day.reply("Red or blue? <!-- attn:state=waiting_input -->")
		waiting := queriedSession(t, day.cli, day.id)
		if waiting.State != protocol.SessionStateWaitingInput {
			t.Fatalf("the day is %s, want waiting_input", waiting.State)
		}
		w.advance(56 * time.Minute)
		if got := pastedContaining(day.term, crewHeartbeat); got != 1 {
			t.Fatalf("a waiting day about to lapse was sent %q, want one heartbeat", day.term.Pasted())
		}
	})
}

func TestAnUntakenHeartbeatLeavesTheContextClockAndTheComposerFree(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		_, day := crewDayInBubble(t, w, nil)
		registerSessions(t, w, day.cli, "sender")
		day.term.OnSubmit(nil)
		before := protocol.Deref(queriedSession(t, day.cli, day.id).LastModelRequestAt)

		w.advance(56 * time.Minute)
		if got := pastedContaining(day.term, crewHeartbeat); got != 1 {
			t.Fatalf("want one heartbeat, got %q", day.term.Pasted())
		}
		w.advance(3 * time.Minute)
		if got := pastedContaining(day.term, crewHeartbeat); got != 1 {
			t.Fatalf("an untaken heartbeat was pasted again: %q", day.term.Pasted())
		}
		if after := protocol.Deref(queriedSession(t, day.cli, day.id).LastModelRequestAt); after != before {
			t.Fatalf("an untaken heartbeat moved the last model request from %s to %s", before, after)
		}
		sendAgentMessage(t, day.cli, "sender", day.id, "the build is green")
		if got := pastedContaining(day.term, inboxDoorbell); got != 1 {
			t.Fatalf("mail after an untaken heartbeat pasted %q, want the doorbell", day.term.Pasted())
		}
	})
}

func TestACrewDayIsAskedOnceToCloseWhenTheUserIsAway(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		_, day := crewDayInBubble(t, w, map[string]string{"crew.away_seconds": "60"})
		day.term.OnSubmit(nil)
		w.advance(56 * time.Minute)
		items := readInbox(t, day.cli, day.id, 0).Items
		if len(items) != 1 || items[0].Content != prompts.RenderText("crew", "sleep-away", prompts.Values{}) {
			t.Fatalf("with the user away the day's inbox holds %q, want one ask to close its day", inboxContents(items))
		}
		w.advance(3 * time.Minute)
		if again := readInbox(t, day.cli, day.id, 0).Items; len(again) != 0 {
			t.Fatalf("the day was asked again minutes after reading the ask: %q", inboxContents(again))
		}
	})
}

func TestACrewDayIsAskedToCloseWhenTheUserIsAwayAndAgainOnlyAfterItsNextWarmWindow(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		_, day := crewDayInBubble(t, w, map[string]string{"crew.away_seconds": "60"})
		sleepAsk := prompts.RenderText("crew", "sleep-away", prompts.Values{})

		w.advance(54 * time.Minute)
		if got := day.term.Pasted(); len(got) != 0 {
			t.Fatalf("a day with a fresh context was sent %q while the user was away", got)
		}
		w.advance(2 * time.Minute)
		if got := pastedContaining(day.term, inboxDoorbell); got != 1 || pastedContaining(day.term, crewHeartbeat) != 0 {
			t.Fatalf("with the user away the day was sent %q, want the inbox doorbell and no heartbeat", day.term.Pasted())
		}
		if got := inboxContents(readInbox(t, day.cli, day.id, 0).Items); got != sleepAsk {
			t.Fatalf("the day's inbox holds %q, want the ask to close its day", got)
		}
		day.reply("Not yet. <!-- attn:state=idle -->")

		w.advance(50 * time.Minute)
		if got := readInbox(t, day.cli, day.id, 0).Items; len(got) != 0 {
			t.Fatalf("the day was asked again inside its warm window: %q", inboxContents(got))
		}
		w.advance(10 * time.Minute)
		if got := inboxContents(readInbox(t, day.cli, day.id, 0).Items); got != sleepAsk {
			t.Fatalf("once its context neared lapsing again the inbox holds %q, want a fresh ask", got)
		}
	})
}

func TestACrewDayMidTurnOrAwaitingApprovalIsLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		enter func(t *testing.T, day *bubbleClaude)
	}{
		{"working", func(*testing.T, *bubbleClaude) {}},
		{"pending_approval", func(t *testing.T, day *bubbleClaude) {
			if err := day.cli.RecordNotification(protocol.TerminalID(day.self), "permission_prompt", "Allow edit?"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, day := crewDayInBubble(t, w, map[string]string{"crew.away_seconds": "60"})
				app.TypeLine(day.id, "refactor the checkout")
				w.advance(0)
				tc.enter(t, day)
				w.advance(0)
				if got := queriedSession(t, day.cli, day.id).State; string(got) != tc.name {
					t.Fatalf("the day is %s, want %s", got, tc.name)
				}
				w.advance(2 * time.Hour)
				if got := day.term.Pasted(); len(got) != 0 {
					t.Fatalf("a day in %s was sent %q", tc.name, got)
				}
			})
		})
	}
}

func TestTheCrewSwitchesTurnOffHeartbeatsAndCloseAsks(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, day := crewDayInBubble(t, w, map[string]string{"crew.heartbeat_enabled": "false"})
		w.advance(56 * time.Minute)
		if got := day.term.Pasted(); len(got) != 0 {
			t.Fatalf("with heartbeats off the day was sent %q", got)
		}
		setSetting(t, app, "crew.autosleep_enabled", "false")
		setSetting(t, app, "crew.away_seconds", "60")
		w.advance(2 * time.Hour)
		if got := day.term.Pasted(); len(got) != 0 {
			t.Fatalf("with close asks off the day was sent %q", got)
		}
	})
}

func TestAHarnessCacheLifetimeSettingMovesTheHeartbeat(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		_, day := crewDayInBubble(t, w, map[string]string{"crew.cache_ttl_seconds.claude": "600"})
		w.advance(4 * time.Minute)
		if got := day.term.Pasted(); len(got) != 0 {
			t.Fatalf("a ten-minute context was nudged at four minutes: %q", got)
		}
		w.advance(2 * time.Minute)
		if got := pastedContaining(day.term, crewHeartbeat); got != 1 {
			t.Fatalf("a ten-minute context five minutes from lapsing was sent %q, want one heartbeat", day.term.Pasted())
		}
	})
}

func TestDeliveryWakesResumeAfterTheWakeLimitWindow(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.wake_limit", "1")
		setSetting(t, app, "crew.wake_limit_window_seconds", "3600")
		registerSessions(t, w, cli, "sender")
		first := sendAgentMessage(t, cli, "sender", "trellis", "the build broke")
		if protocol.Deref(first.TargetSessionID) == "" || !strings.Contains(first.Detail, "woke Trellis") {
			t.Fatalf("first wake=%+v", first)
		}
		w.terminal(string(protocol.Deref(first.TargetSessionID))).Exit(0)
		w.advance(30 * time.Minute)
		inside := sendAgentMessage(t, cli, "sender", "trellis", "the build broke again")
		if inside.Status != protocol.AgentMsgStatusQueued || !strings.Contains(inside.Detail, "crew.wake_limit=1") {
			t.Fatalf("inside limit window=%+v", inside)
		}
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("limit woke %s", *binding)
		}
		w.advance(31 * time.Minute)
		fresh := sendAgentMessage(t, cli, "sender", "trellis", "the build broke a third time")
		if protocol.Deref(fresh.TargetSessionID) == "" || !strings.Contains(fresh.Detail, "woke Trellis") {
			t.Fatalf("after limit window=%+v", fresh)
		}
	})
}
