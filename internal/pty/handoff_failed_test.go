package pty

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

func TestAFailedHandoffLeavesTheSessionOutputFlowing(t *testing.T) {
	m := NewManager(nil)
	const id = "stuck-subscriber"
	if err := m.Spawn(SpawnOptions{ID: id, CWD: t.TempDir(), Agent: "codex", Cols: 80, Rows: 24, AgentArgv: []string{"cat"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Remove(id) })

	stuck := make(chan struct{})
	release := make(chan struct{})
	after := make(chan struct{})
	var once, afterOnce sync.Once
	var seen bytes.Buffer
	send := func(data []byte, _ uint32) bool {
		once.Do(func() {
			close(stuck)
			<-release
		})
		seen.Write(data)
		if bytes.Contains(seen.Bytes(), []byte("after-handoff")) {
			afterOnce.Do(func() { close(after) })
		}
		return true
	}
	if _, err := m.Subscribe(id, "stuck", send, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := m.Input(ctx, id, []byte("before-handoff\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stuck:
	case <-ctx.Done():
		t.Fatal("the subscriber never received the first output")
	}
	if _, err := m.Handoff(id); err == nil {
		t.Fatal("a handoff past a stuck subscriber succeeded")
	}
	close(release)

	if err := m.Input(ctx, id, []byte("after-handoff\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-after:
	case <-ctx.Done():
		t.Fatal("the session went silent after the failed handoff")
	}
}
