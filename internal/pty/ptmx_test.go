package pty

import (
	"context"
	"testing"
	"time"
)

func TestClosingATerminalHangsUpItsChildWhileLaterTerminalsRun(t *testing.T) {
	m := NewManager(nil)
	exited := make(chan string, 2)
	m.SetExitHandler(func(info ExitInfo) { exited <- info.ID })
	for _, id := range []string{"a", "b"} {
		if err := m.Spawn(SpawnOptions{ID: id, CWD: t.TempDir(), Agent: "codex", AgentArgv: []string{"sleep", "30"}}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { m.Remove("a"); m.Remove("b") })

	m.Remove("a")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case id := <-exited:
		if id != "a" {
			t.Fatalf("session %s exited instead of a", id)
		}
	case <-ctx.Done():
		t.Fatal("closing a's terminal did not hang up its child: a later terminal's child holds a's pty master")
	}
}
