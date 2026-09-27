package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func addChiefOfStaffTestSession(d *Daemon, id, label string) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: label, Agent: protocol.SessionAgentCodex,
		Directory: "/tmp/" + id, WorkspaceID: "workspace-" + id,
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestTypeDoorbellDelaysEnterAfterThePaste(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	addChiefOfStaffTestSession(d, "delayed-enter", "target")

	const gap = 40 * time.Millisecond
	previous := sessionInputSubmitDelay
	sessionInputSubmitDelay = gap
	t.Cleanup(func() { sessionInputSubmitDelay = previous })

	var mu sync.Mutex
	var writes []string
	var at []time.Time
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		writes = append(writes, string(data))
		at = append(at, time.Now())
	}}

	delivery := maintenanceSessionInput("input-test", "delayed-enter", "delayed-enter", "ping", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("session input error = %v", attempt.err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantPaste := sessionInputPasteStart + "ping" + sessionInputPasteEnd
	if len(writes) != 2 || writes[0] != wantPaste || writes[1] != "\r" {
		t.Fatalf("PTY writes = %q, want [%q, %q]", writes, wantPaste, "\r")
	}
	if elapsed := at[1].Sub(at[0]); elapsed < gap {
		t.Fatalf("Enter followed the paste after %v, want at least %v", elapsed, gap)
	}
}
