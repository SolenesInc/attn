package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASessionContextWindowCapRelaunchesTheAgentAndIsRefusedForAShellOrOutOfBounds(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "")
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	agent := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(agent)
	shell := w.Spawn(app, shellHarness, w.Path("docs"))
	pin := func(session string, tokens int) protocol.SessionContextWindowCapResultMessage {
		t.Helper()
		return testworld.Request(app, protocol.SetSessionContextWindowCapMessage{Cmd: protocol.CmdSetSessionContextWindowCap, SessionID: protocol.SessionID(session), Cap: tokens},
			protocol.EventSessionContextWindowCapResult, func(r protocol.SessionContextWindowCapResultMessage) bool { return string(r.SessionID) == session })
	}

	if got := pin(shell, 200000); got.Success {
		t.Errorf("a shell accepted a context window cap it can never apply: %+v", got)
	}
	for _, tokens := range []int{-1, 1, 9000000} {
		if got := pin(agent, tokens); got.Success {
			t.Errorf("the agent accepted an out-of-bounds cap %d: %+v", tokens, got)
		}
	}
	if got := pin(agent, 500000); !got.Success {
		t.Fatalf("pin the agent to 500000: %s", protocol.Deref(got.Error))
	}
	if got := compactWindow(w.Launched(agent)); got != "500000" {
		t.Errorf("the pinned agent relaunched with window %q, want 500000", got)
	}
	if got := pin(agent, 0); !got.Success {
		t.Fatalf("clear the agent's pin: %s", protocol.Deref(got.Error))
	}
	if got := compactWindow(w.Launched(agent)); got != "" {
		t.Errorf("the unpinned agent relaunched with window %q, want none", got)
	}
}

func TestTheHeadlessContextWindowCapReachesEachHeadlessTask(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "")
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	titled := func(dir string) string {
		t.Helper()
		w.Spawn(app, fakeagent.Claude, w.Path(dir), func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("investigate the retry queue")
		})
		task := w.HeadlessTask()
		task.Answer("Retry queue investigation")
		window := ""
		for _, pair := range task.Env {
			if value, ok := strings.CutPrefix(pair, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="); ok {
				window = value
			}
		}
		return window
	}

	if got := titled("shop"); got != "128000" {
		t.Errorf("with no headless cap configured a headless task runs with window %q, want the default 128000", got)
	}
	setSetting(t, app, "headless_context_window_cap", "180000")
	if got := titled("docs"); got != "180000" {
		t.Errorf("after the headless cap is set to 180000 a headless task runs with window %q", got)
	}
}
