package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAHandoffWhileTheUserIsAwayEndsTheDayUnlessItAsksForANap(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		for _, member := range []string{"trellis", "alder"} {
			writeCrewCharter(t, w, member)
		}
		w.restart()
		app, cli := w.App(), w.Client()
		for key, value := range map[string]string{"crew.wake_limit": "0", "crew.heartbeat_enabled": "false", "crew.autosleep_enabled": "false"} {
			setSetting(t, app, key, value)
		}
		for _, member := range []string{"trellis", "alder"} {
			if err := w.InjectCrewSession(member+"-day", member+"-day", w.Path(member+"-day"), member); err != nil {
				t.Fatal(err)
			}
		}
		app.Close()
		w.advance(150 * time.Minute)
		presenceTierIs(t, cli, "with no app since startup", "away")

		ended := crewHandoff(t, cli, "trellis-day", "Nobody is around; resting.", false, "")
		if protocol.Deref(ended.Outcome) != protocol.CrewDayCloseSleep || ended.SessionID != nil || ended.NapError != nil {
			t.Fatalf("a handoff while the user is away = %+v, want the day ended with nobody woken behind it", ended)
		}
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("trellis is still bound to %s after its day ended", *binding)
		}
		if letters := crewLetters(t, w, "trellis"); len(letters) != 1 {
			t.Fatalf("trellis's letters = %q, want the one just filed", letters)
		}

		napped := crewHandoff(t, cli, "alder-day", "Carry on without the user.", false, protocol.CrewDayCloseNap)
		if protocol.Deref(napped.Outcome) == protocol.CrewDayCloseSleep || !strings.Contains(protocol.Deref(napped.NapError), "crew.wake_limit=0") {
			t.Fatalf("an explicit nap while the user is away = %+v, want it to try to wake the successor", napped)
		}
		if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != "alder-day" {
			t.Fatalf("after the refused nap alder is bound to %q, want its running day", got)
		}
	})
}

func TestNapsWithoutAnAppUseTheAutonomousWakeBudget(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		for key, value := range map[string]string{"crew.wake_limit": "1", "crew.heartbeat_enabled": "false", "crew.autosleep_enabled": "false"} {
			setSetting(t, app, key, value)
		}
		app.Close()
		day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		w.advance(150 * time.Minute)
		napped := crewHandoff(t, cli, day.id, "Carry on without the user.", false, protocol.CrewDayCloseNap)
		if protocol.Deref(napped.Outcome) != protocol.CrewDayCloseNap || napped.NapError != nil || napped.SessionID == nil {
			t.Fatalf("the first unattended nap = %+v, want a successor", napped)
		}
		next := w.bootBubbleClaude(t, string(*napped.SessionID))
		next.reply("Ready again. <!-- attn:state=idle -->")
		w.advance(time.Minute)
		limited := crewHandoff(t, cli, next.id, "Try another successor.", false, protocol.CrewDayCloseNap)
		if limited.SessionID != nil || !strings.Contains(protocol.Deref(limited.NapError), "crew.wake_limit=1") {
			t.Fatalf("the second unattended nap = %+v, want the used wake budget to refuse it", limited)
		}
	})
}
