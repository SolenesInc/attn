package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func BenchmarkLocalPtyInput(b *testing.B) {
	w := &world{World: prepareWorld(b), terms: testworld.NewTerminals()}
	w.start()
	defer w.stop()
	app := w.App()
	session := w.Spawn(app, shellHarness, w.Path("shell"))
	input := protocol.PtyInputMessage{
		Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(session)),
		Data: "x", ProbeID: protocol.Ptr("benchmark"),
	}
	write := func() {
		mark := app.EventCount()
		app.Send(input)
		result := testworld.AwaitSince(app, mark, protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool {
			return r.ProbeID == "benchmark"
		})
		if !result.Success {
			b.Fatal(protocol.Deref(result.Error))
		}
	}
	write()
	b.SetBytes(int64(len(input.Data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		write()
	}
	b.StopTimer()
}
