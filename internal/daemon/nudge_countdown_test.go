package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSessionInputWriteDoesNotInterleaveWithPendingApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := "doorbell-state-fence"
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          "doorbell-state-fence",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	inputStarted := make(chan struct{})
	releaseInput := make(chan struct{})
	inputs := make(chan string, 2)
	var firstWrite sync.Once
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		inputs <- string(data)
		firstWrite.Do(func() {
			close(inputStarted)
			<-releaseInput
		})
	}}

	inputDone := make(chan error, 1)
	delivery := maintenanceSessionInput("input-test", "countdown-splice", sessionID, ticketNudgePrompt, sessionInputAtTurnBoundary)
	go func() { inputDone <- d.sessionInputs().try(context.Background(), delivery).err }()
	<-inputStarted

	stateDone := make(chan struct{})
	go func() {
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StatePendingApproval,
			cause:     resolverObservation{},
		})
		close(stateDone)
	}()
	select {
	case <-stateDone:
		t.Fatal("pending approval committed while a doorbell input was in flight")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseInput)
	if err := <-inputDone; err != nil {
		t.Fatalf("session input error = %v", err)
	}
	<-stateDone
	wantPaste := sessionInputPasteStart + ticketNudgePrompt + sessionInputPasteEnd
	if got := <-inputs; got != wantPaste {
		t.Fatalf("doorbell paste = %q, want %q", got, wantPaste)
	}
	if got := <-inputs; got != "\r" {
		t.Fatalf("session-input submit = %q, want a lone Enter", got)
	}
}
