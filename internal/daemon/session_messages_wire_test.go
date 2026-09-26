package daemon_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheMessageWindowShowsEachSessionsOwnAnswersOldestFirstUnderStableKeys(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Codex, cwd)
	firstRun := w.Launched(first)
	if window := messageWindow(app, first); !window.Success || window.Status != protocol.SessionMessageWindowStatusReady || len(window.Messages) != 0 {
		t.Fatalf("a fresh session's window = %+v, want a ready, empty window", window)
	}
	second := w.Spawn(app, fakeagent.Codex, cwd)
	secondRun := w.Launched(second)

	app.TypeLine(first, "plan the discount field")
	firstRun.Prompted()
	firstRun.Reply("An earlier answer. <!-- attn:state=idle -->")
	app.TypeLine(second, "rename the checkout module")
	secondRun.Prompted()
	secondRun.Reply("The other session's answer. <!-- attn:state=idle -->")
	app.TypeLine(first, "build it")
	firstRun.Prompted()
	firstRun.Reply("The answer under annotation. <!-- attn:state=idle -->")

	window := messageWindowShowing(app, first, "The answer under annotation.")
	if got := messageMarkdowns(window); !slices.Equal(got, []string{"An earlier answer.", "The answer under annotation."}) || window.Truncated || window.SessionID != first {
		t.Fatalf("the first session's window = %q (truncated %v), want its own two answers oldest first", got, window.Truncated)
	}
	again := messageWindowShowing(app, first, "The answer under annotation.")
	if window.Messages[0].Key == "" || window.Messages[0].Key == window.Messages[1].Key {
		t.Errorf("message keys = %q and %q, want one per message", window.Messages[0].Key, window.Messages[1].Key)
	}
	for i := range window.Messages {
		if again.Messages[i].Key != window.Messages[i].Key {
			t.Errorf("message %d changed its key from %q to %q across reads", i, window.Messages[i].Key, again.Messages[i].Key)
		}
	}
	if got := messageMarkdowns(messageWindowShowing(app, second, "The other session's answer.")); !slices.Equal(got, []string{"The other session's answer."}) {
		t.Errorf("the second session's window = %q, want only its own answer", got)
	}

	for _, id := range []string{"", "nope"} {
		refused := requestMessageWindow(app, id, "unknown-"+id)
		if refused.Success || protocol.Deref(refused.Error) == "" {
			t.Errorf("the window of session %q = %+v, want a refusal", id, refused)
		}
	}
}

func TestTheMessageWindowKeepsTheNewestAnswersWithinItsBudgetAndSaysItDroppedSome(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "write the release notes")
	codex.Prompted()

	codex.Reply("A normal answer.")
	codex.Reply(strings.Repeat("x", 64*1024+1))
	codex.Reply("An answer after the oversize one.")
	oversize := messageWindowShowing(app, session, "An answer after the oversize one.")
	if got := messageMarkdowns(oversize); !slices.Equal(got, []string{"A normal answer.", "An answer after the oversize one."}) || !oversize.Truncated {
		t.Fatalf("after an oversize answer the window holds %d answers (truncated %v), want only the one in budget and a truncation", len(got), oversize.Truncated)
	}

	var answers []string
	for i := range 35 {
		answer := fmt.Sprintf("answer %d", i)
		codex.Reply(answer)
		answers = append(answers, answer)
	}
	capped := messageWindowShowing(app, session, answers[len(answers)-1])
	if got := messageMarkdowns(capped); !slices.Equal(got, answers[len(answers)-32:]) || !capped.Truncated {
		t.Errorf("the window holds %q (truncated %v), want the newest 32 answers and a truncation", got, capped.Truncated)
	}
}

func TestTheMessageWindowSaysWhenItIsStillLookingAndWhenThereIsNoTranscript(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	boot := w.HoldNextBoot()
	booting := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	discovering := requestMessageWindow(app, booting, "booting")
	boot()
	w.Launched(booting)
	if !discovering.Success || discovering.Status != protocol.SessionMessageWindowStatusDiscovering || len(discovering.Messages) != 0 || discovering.Truncated {
		t.Errorf("a booting agent's window = %+v, want a successful, empty discovering window", discovering)
	}

	shell := w.Spawn(app, fakeagent.Harness(protocol.AgentShellValue), w.Path("shell"))
	unavailable := requestMessageWindow(app, shell, "shell")
	if !unavailable.Success || unavailable.Status != protocol.SessionMessageWindowStatusUnavailable || protocol.Deref(unavailable.Detail) == "" {
		t.Errorf("a shell's window = %+v, want a successful unavailable window that says why", unavailable)
	}
}

func requestMessageWindow(app *testworld.Peer, session, requestID string) protocol.SessionMessagesGetResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.SessionMessagesGetMessage{
		Cmd: protocol.CmdSessionMessagesGet, RequestID: requestID, SessionID: session,
	}, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == requestID })
}

func messageWindowShowing(app *testworld.Peer, session, newest string) protocol.SessionMessagesGetResultMessage {
	app.T.Helper()
	for read := 0; ; read++ {
		window := requestMessageWindow(app, session, fmt.Sprintf("%s-%d", session, read))
		if got := messageMarkdowns(window); len(got) > 0 && got[len(got)-1] == newest {
			return window
		}
		testworld.Await(app, protocol.EventSessionMessagesChanged, func(e protocol.SessionMessagesChangedMessage) bool { return e.SessionID == session })
	}
}

func messageMarkdowns(window protocol.SessionMessagesGetResultMessage) []string {
	out := make([]string, 0, len(window.Messages))
	for _, message := range window.Messages {
		out = append(out, message.Markdown)
	}
	return out
}
