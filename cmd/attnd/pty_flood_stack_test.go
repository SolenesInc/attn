package main_test

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// Twice the host's 16 MiB subscriber queue (pty-host/src/outbox.rs SUBSCRIBER_QUEUE_MAX_BYTES).
const floodBytes = 32 << 20

func TestATerminalWhoseDaemonFallsBehindAFloodIsToldToResync(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	reading := s.PauseEvery(pausepoint.PtyStreamRead)
	s.Start()
	flooding := make(chan struct{})
	passedThrough := passReads(reading, flooding)
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
	flooded := filepath.Join(s.Dir, "flooded")
	if err := syscall.Mkfifo(flooded, 0o600); err != nil {
		t.Fatal(err)
	}
	close(flooding)
	<-passedThrough
	app.TypeLine(shell, "yes flood | head -c "+strconv.Itoa(floodBytes)+"; exec 3>"+flooded)

	reading.Await()
	openHeldPipe(t, flooded)
	reading.Release()
	resynced := make(chan struct{})
	drained := passReads(reading, resynced)
	terminal := app.Terminal(shell)
	testworld.Await(app, protocol.EventPtyDesync, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.ID) == terminal && protocol.Deref(e.Reason) == "runtime_stream_ended"
	})
	close(resynced)
	<-drained
	_ = os.Remove(flooded)
}

func passReads(reading *testworld.Pause, until <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for reading.AwaitBefore(until) {
			reading.Release()
		}
	}()
	return done
}
