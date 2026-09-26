package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOnlyProbedPtyInputIsAcknowledged(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	session := w.Spawn(app, workspaceShell, w.Path("shop"))
	testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: session},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == session })

	for _, input := range []struct{ data, probe string }{
		{"printf 'mark%s\\n' er-probed\r", "probe-1"},
		{"printf 'mark%s\\n' er-plain\r", ""},
		{"printf 'mark%s\\n' er-again\r", "probe-2"},
	} {
		msg := protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: session, Data: input.data, Source: protocol.Ptr("automation")}
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
		if result.ID != session || !result.Success || result.WriteDurationUs < 0 {
			t.Errorf("probe result %+v, want a successful write to %s with its duration", result, session)
		}
	}
}
