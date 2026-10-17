package daemon_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestANotebookEntryForAChiefHeldByTheUsersTypingLandsOnceTheyAreQuiet(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		chief := w.bubbleClaude(t, app, "chief")
		if set := setChiefOfStaff(app, chief.id, true); !set.Success {
			t.Fatalf("make %s the chief: %s", chief.id, protocol.Deref(set.Error))
		}
		synctest.Wait()
		chief = w.bootBubbleClaude(t, chief.id)

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(chief.self), Data: "half a thought"})
		sent := notebookAskSendToChief(app, "notes/today.md", "follow up on the release")
		if !sent.Success || sent.Result == nil || sent.Result.Nudged {
			t.Fatalf("sending to a chief the user is typing to = %+v, want it written and not nudged yet", sent)
		}
		w.advance(29 * time.Second)
		if pasted := chief.term.Pasted(); len(pasted) != 0 {
			t.Fatalf("the chief nudge pasted %q into a composer the user typed in 29s ago", pasted)
		}
		w.advance(time.Second)
		if pasted := chief.term.Pasted(); len(pasted) != 1 || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet the chief was nudged with %q, want the inbox doorbell", pasted)
		}
	})
}
