package pty

import (
	"testing"
	"time"
)

func TestHandoffRefusesAnExitedSession(t *testing.T) {
	const id = "handoff-dead"
	m := NewManager(nil)
	t.Cleanup(m.Shutdown)
	exits := make(chan ExitInfo, 1)
	m.SetExitHandler(func(info ExitInfo) { exits <- info })
	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-dead",
		ExternalCommand: []string{"/bin/sh", "-c", "exit 3"},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	select {
	case <-exits:
	case <-time.After(10 * time.Second):
		t.Fatal("the fixture child never exited")
	}
	if _, err := m.Handoff(id); err == nil {
		t.Fatal("Handoff() accepted a session whose child is gone")
	}
}
