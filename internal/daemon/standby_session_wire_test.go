package daemon_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
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

func TestStandbyUntilRightAfterAnIdleSigmaForkAsksTheSelectedSession(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	first, tui, server := appServerSession(t, w, app, fakeagent.Sigma)
	terminal := app.Terminal(first)
	original := tui.ConversationID
	settleTwoSigmaPrompts(app, first, tui, server)

	typeInto(app, terminal, "/fork\r")
	tui.Prompted()
	if forks := server.Forks(original); len(forks) != 1 {
		t.Fatalf("the /fork made %v, want one fork", forks)
	}
	app.Send(protocol.StandbySessionMessage{Cmd: protocol.CmdStandbySession, SessionID: protocol.SessionID(first), Until: time.Now().Add(time.Hour)})
	if got := server.Prompted(original); !strings.Contains(got.Text, "standby") {
		t.Fatalf("the selected session's conversation %s took %+v, want the standby request", original, got)
	}
}
