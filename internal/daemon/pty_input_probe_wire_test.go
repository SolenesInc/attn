package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOnlyProbedPtyInputIsAcknowledged(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	session := w.Spawn(app, shellHarness, w.Path("shop"))
	terminal := app.Terminal(session)
	testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })

	for _, input := range []struct{ data, probe string }{
		{"printf 'mark%s\\n' er-probed\r", "probe-1"},
		{"printf 'mark%s\\n' er-plain\r", ""},
		{"printf 'mark%s\\n' er-again\r", "probe-2"},
	} {
		msg := protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: input.data, Source: protocol.Ptr("automation")}
		if input.probe != "" {
			msg.ProbeID = protocol.Ptr(input.probe)
		}
		app.Send(msg)
	}
	second := testworld.Await(app, protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == "probe-2" })
	first := testworld.Await(app, protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == "probe-1" })
	app.AwaitScreen(session, "marker-again")

	var acknowledged []protocol.WebSocketEvent
	for _, e := range app.Received() {
		if e.Event == protocol.EventPtyInputProbeResult {
			acknowledged = append(acknowledged, e)
		}
	}
	if len(acknowledged) != 2 {
		t.Fatalf("three inputs, two of them probed, drew %d probe results, want 2", len(acknowledged))
	}
	for _, result := range []protocol.PtyInputProbeResultMessage{first, second} {
		if string(result.ID) != terminal || !result.Success || result.WriteDurationUs < 0 {
			t.Errorf("probe result %+v, want a successful write to %s with its duration", result, terminal)
		}
	}
}

func TestProbedInputToAStoppedAgentAnswersThatItFailed(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	terminal := protocol.TerminalID(app.Terminal(session))
	w.Launched(session).Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })

	result := testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "ship it", Source: protocol.Ptr("user"), ProbeID: protocol.Ptr("after-exit")},
		protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == "after-exit" })

	if result.Success || protocol.Deref(result.Error) == "" {
		t.Fatalf("input to a stopped agent answered %+v, want a failure naming why", result)
	}
}
