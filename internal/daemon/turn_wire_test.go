package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATurnSettledInTheSecondItOpenedInReopensWhenTheSessionIsDueAgain(t *testing.T) {
	for _, c := range []struct {
		name            string
		opened, settled time.Duration
	}{
		{"opened on a whole second", 0, 500 * time.Millisecond},
		{"settled a fraction after it opened", 123400 * time.Microsecond, 123450 * time.Microsecond},
	} {
		t.Run(c.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
					t.Fatalf("register: %v", err)
				}
				if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
					t.Fatalf("report waiting_input: %v", err)
				}
				testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

				second := time.Now().Truncate(time.Second).Add(2 * time.Second)
				dueAgainAt := second.Add(900 * time.Millisecond)
				snoozeUntil(app, "s1", second.Add(c.opened))
				w.advance(time.Until(second.Add(c.settled)))
				snoozeUntil(app, "s1", dueAgainAt)
				w.advance(time.Until(dueAgainAt))

				testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
					return protocol.Deref(s.TurnOwed) && openedAt(s).Equal(dueAgainAt)
				})
			})
		})
	}
}

func snoozeUntil(app *testworld.Peer, sessionID string, until time.Time) {
	app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: sessionID, Until: until.Format(time.RFC3339Nano)})
}
