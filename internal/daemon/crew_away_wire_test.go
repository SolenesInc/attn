package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAHandoffWhileTheUserIsAwayEndsTheDay(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		for key, value := range map[string]string{"crew.wake_limit": "0", "crew.heartbeat_enabled": "false", "crew.autosleep_enabled": "false"} {
			setSetting(t, app, key, value)
		}
		if err := w.InjectCrewSession("trellis-day", "trellis-day", w.Path("trellis-day"), "trellis"); err != nil {
			t.Fatal(err)
		}
		app.Close()
		w.advance(150 * time.Minute)
		presenceTierIs(t, cli, "with no app since startup", "away")

		ended := crewHandoff(t, cli, "trellis-day", "Nobody is around; resting.", false)
		if protocol.Deref(ended.Outcome) != protocol.CrewDayCloseSleep || ended.SessionID != nil || ended.SuccessorError != nil {
			t.Fatalf("a handoff while the user is away = %+v, want the day ended with nobody woken behind it", ended)
		}
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("trellis is still bound to %s after its day ended", *binding)
		}
		if letters := crewLetters(t, w, "trellis"); len(letters) != 1 {
			t.Fatalf("trellis's letters = %q, want the one just filed", letters)
		}

	})
}

func TestSuccessorsWithoutAnAppUseTheAutonomousWakeBudget(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		for key, value := range map[string]string{"crew.wake_limit": "1", "crew.heartbeat_enabled": "false", "crew.autosleep_enabled": "false"} {
			setSetting(t, app, key, value)
		}
		app.Close()
		day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "codex").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		applyCrew(t, cli, "trellis")
		w.advance(150 * time.Minute)
		switched := crewHandoff(t, cli, day.id, "Carry on without the user.", false)
		if protocol.Deref(switched.Outcome) != protocol.CrewDayCloseSuccessor || switched.SuccessorError != nil || switched.SessionID == nil {
			t.Fatalf("the first unattended successor = %+v, want it started", switched)
		}
		next := w.bootBubbleClaude(t, string(*switched.SessionID))
		next.reply("Ready again. <!-- attn:state=idle -->")
		switchCrewHarness(t, cli, "trellis", "codex")
		w.advance(time.Minute)
		limited := crewHandoff(t, cli, next.id, "Try another successor.", false)
		if limited.SessionID != nil || !strings.Contains(protocol.Deref(limited.SuccessorError), "crew.wake_limit=1") {
			t.Fatalf("the second unattended successor = %+v, want the used wake budget to refuse it", limited)
		}
	})
}
