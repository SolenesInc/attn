package daemon_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestASeedSentToAChiefHeldByTheUsersTypingRingsOnceTheyAreQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		chief := w.bubbleClaude(t, app, "chief")
		if set := setChiefOfStaff(app, chief.id, true); !set.Success {
			t.Fatalf("make %s the chief: %s", chief.id, protocol.Deref(set.Error))
		}
		synctest.Wait()
		chief = w.bootBubbleClaude(t, chief.id)
		registerSessions(t, w, cli, "sender")
		seed := plantSeedAs(t, cli, "sender", "Follow up on the release")
		tended := lifeMove(t, cli, "sender", seed, "tend", "", "")
		w.advance(0)

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(chief.self), Data: "half a thought"})
		synctest.Wait()
		if _, err := cli.SeedSendToChief("sender", tended, ""); err != nil {
			t.Fatalf("send %s to the chief: %v", seed, err)
		}
		w.advance(29 * time.Second)
		if pasted := chief.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("the chief was rung with %q into a composer the user typed in 29s ago", pasted)
		}
		w.advance(time.Second)
		if pasted := chief.term.Pasted(); len(pasted) != 1 || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet the chief was rung with %q, want the inbox doorbell", pasted)
		}
	})
}
