package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATerminalWhoseDaemonFallsBehindAFloodIsToldToResync(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	reading := s.PauseAt(pausepoint.PtyStreamRead)
	dropped := s.PauseAt(pausepoint.PtySubscriberDrop)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
	flood := filepath.Join(s.Dir, "flood.sh")
	if err := os.WriteFile(flood, []byte("while [ ! -e \"$1\" ]; do echo flood; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stop := filepath.Join(s.Dir, "stop-flood")
	app.TypeLine(shell, "sh "+flood+" "+stop)

	reading.Await()
	dropped.Await()
	reading.Release()
	dropped.Release()
	testworld.Await(app, protocol.EventPtyDesync, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.ID) == shell && protocol.Deref(e.Reason) == "buffer_overflow"
	})
	if err := os.WriteFile(stop, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
