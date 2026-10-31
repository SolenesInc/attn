package pty

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSpawnedChildDoesNotInheritEarlierMasters(t *testing.T) {
	m := NewManager(nil)
	spawn := func(id string) int {
		if err := m.Spawn(SpawnOptions{ID: id, CWD: t.TempDir(), Agent: "codex", AgentArgv: []string{"sleep", "30"}}); err != nil {
			t.Fatal(err)
		}
		info, err := m.SessionInfo(id)
		if err != nil {
			t.Fatal(err)
		}
		return info.PID
	}
	spawn("a")
	t.Cleanup(func() { m.Remove("a"); m.Remove("b") })
	pidB := spawn("b")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pidB))
		if err == nil {
			for _, e := range entries {
				l, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pidB, e.Name()))
				if strings.Contains(l, "ptmx") {
					t.Fatalf("child of session b inherited pty master fd %s -> %s", e.Name(), l)
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
