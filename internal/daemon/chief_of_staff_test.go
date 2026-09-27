package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func readChiefOfStaffResult(t *testing.T, client *wsClient) protocol.ChiefOfStaffResultMessage {
	t.Helper()
	select {
	case raw := <-client.send:
		var result protocol.ChiefOfStaffResultMessage
		if err := json.Unmarshal(raw.payload, &result); err != nil {
			t.Fatalf("decode chief_of_staff_result: %v", err)
		}
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chief_of_staff_result")
		return protocol.ChiefOfStaffResultMessage{}
	}
}

func newChiefOfStaffTestDaemon(t *testing.T) (*Daemon, *wsClient) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	return d, newRenameTestClient()
}

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

func TestTypeDoorbellDoesNotSubmitInputRacingTheGap(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	addChiefOfStaffTestSession(d, "splice-race", "target")

	previous := sessionInputSubmitDelay
	sessionInputSubmitDelay = 60 * time.Millisecond
	t.Cleanup(func() { sessionInputSubmitDelay = previous })

	var mu sync.Mutex
	var writes []string
	var writtenAt []time.Time
	pasteSeen := make(chan struct{})
	var pasteOnce sync.Once
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		writes = append(writes, string(data))
		writtenAt = append(writtenAt, time.Now())
		mu.Unlock()
		if strings.HasPrefix(string(data), sessionInputPasteStart) {
			pasteOnce.Do(func() { close(pasteSeen) })
		}
	}}

	inputDone := make(chan error, 1)
	delivery := maintenanceSessionInput("input-test", "splice-race", "splice-race", "ping", sessionInputAtTurnBoundary)
	go func() { inputDone <- d.sessionInputs().try(context.Background(), delivery).err }()

	<-pasteSeen
	typed := make(chan struct{})
	var typedAt time.Time
	go func() {
		defer close(typed)
		typedAt = time.Now()
		d.handlePtyInput(&wsClient{send: make(chan outboundMessage, 4)}, &protocol.PtyInputMessage{
			Cmd: protocol.CmdPtyInput, ID: "splice-race", Data: "half-typed line",
		})
	}()

	if err := <-inputDone; err != nil {
		t.Fatalf("session input error = %v", err)
	}
	<-typed

	mu.Lock()
	defer mu.Unlock()
	wantPaste := sessionInputPasteStart + "ping" + sessionInputPasteEnd
	want := []string{wantPaste, "\r", "half-typed line"}
	if len(writes) != len(want) {
		t.Fatalf("PTY writes = %q, want %q", writes, want)
	}
	for i := range want {
		if writes[i] != want[i] {
			t.Fatalf("PTY writes = %q, want %q (the keystroke was spliced into the submission)", writes, want)
		}
	}
	if !typedAt.Before(writtenAt[1]) {
		t.Fatalf("keystroke started at %v, after the Enter at %v — the race never happened", typedAt, writtenAt[1])
	}
}

func chiefWasNudged(inputs []string, prompt string) bool {
	for _, in := range inputs {
		if strings.Contains(in, prompt) {
			return true
		}
	}
	return false
}

func TestNudgeChiefOfStaffHeldOffByTypingLandsAfterTheQuietWindow(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	chiefID, _, inputs := delegateForNotify(t, d, "codex")
	prompt := "the notebook inbox has a new entry"
	quiesceTranscriptWatchers(t, d)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(chiefID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		if d.nudgeChiefOfStaff("inbox-1", prompt) {
			t.Fatal("the nudge claimed a composer the user had just used")
		}
		if chiefWasNudged(inputs(chiefID), agentMailboxDoorbellText) {
			t.Fatalf("typed into a composer the user just used: %q", inputs(chiefID))
		}

		time.Sleep(sessionInputQuietWindow)
		settleResend(t)
		if !chiefWasNudged(inputs(chiefID), agentMailboxDoorbellText) {
			t.Fatalf("nothing resent the chief nudge once the composer went quiet: %q", inputs(chiefID))
		}
	})
}
