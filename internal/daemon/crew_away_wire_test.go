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
		for key, value := range map[string]string{"crew.away_seconds": "60", "crew.wake_limit": "0", "crew.heartbeat_enabled": "false", "crew.autosleep_enabled": "false"} {
			setSetting(t, app, key, value)
		}
		for _, member := range []string{"trellis", "alder"} {
			if err := w.InjectCrewSession(member+"-day", member+"-day", w.Path(member+"-day"), member); err != nil {
				t.Fatal(err)
			}
		}
		w.advance(2 * time.Minute)

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
