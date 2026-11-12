package daemon_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestStandbyUntilAsksTheAgentToPutItsSeedOnStandbyUntilThatTime(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		claude := w.bubbleClaude(t, app, "shop")
		until := time.Now().Add(2 * time.Hour)

		app.Send(protocol.StandbySessionMessage{Cmd: protocol.CmdStandbySession, SessionID: protocol.SessionID(claude.id), Until: until})
		w.advance(time.Second)
		synctest.Wait()

		typed := ""
		for _, input := range claude.term.Inputs() {
			typed += input.Data
		}
		if want := "attn seed standby <seed> --at " + until.Format(time.RFC3339); !strings.Contains(typed, want) || !strings.HasSuffix(typed, "\r") {
			t.Fatalf("attn typed %q into the session, want a submitted request to run %q", typed, want)
		}
	})
}
