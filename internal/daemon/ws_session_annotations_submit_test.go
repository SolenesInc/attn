package daemon

import (
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const submitAnnotationText = "Feedback on your last message.\n\n## 1. 🔍 Verify this\n\n> the parser already handles this"

func sendAnnotationSubmit(t *testing.T, d *Daemon, sessionID, text string) protocol.SessionAnnotationsSubmitResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleSessionAnnotationsSubmit(client, &protocol.SessionAnnotationsSubmitMessage{
		Cmd:       protocol.CmdSessionAnnotationsSubmit,
		SessionID: sessionID,
		Text:      text,
		RequestID: "req-1",
	})
	var res protocol.SessionAnnotationsSubmitResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Event != protocol.EventSessionAnnotationsSubmitResult || res.RequestID != "req-1" {
		t.Fatalf("unexpected result envelope: %+v", res)
	}
	return res
}

// The Enter that submits arrives as a SEPARATE PTY write: folded into the bracketed-paste
// block it would be pasted text, and the feedback would sit in the composer forever.
func TestSessionAnnotationsSubmitDelivers(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "session-1", protocol.SessionStateIdle)

	res := sendAnnotationSubmit(t, d, "session-1", submitAnnotationText)

	if !res.Success || res.Status != annotationSubmitStatusDelivered || res.Error != nil {
		t.Fatalf("result = %+v, want delivered", res)
	}
	wantPaste := sessionInputPasteStart + submitAnnotationText + sessionInputPasteEnd
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 || inputs[0] != wantPaste || inputs[1] != "\r" {
		t.Fatalf("PTY inputs = %q, want [%q, %q]", inputs, wantPaste, "\r")
	}
}

func TestSessionAnnotationsSubmitSkipsPendingApproval(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "session-1", protocol.SessionStatePendingApproval)

	res := sendAnnotationSubmit(t, d, "session-1", submitAnnotationText)

	if res.Success || res.Status != annotationSubmitStatusSkipped || res.Error != nil {
		t.Fatalf("result = %+v, want skipped_pending_approval", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("nothing may be typed into a session on an approval prompt, got %q", inputs)
	}
}

func TestSessionAnnotationsSubmitUnknownSession(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)

	res := sendAnnotationSubmit(t, d, "nope", submitAnnotationText)

	if res.Success || res.Status != annotationSubmitStatusError ||
		res.Error == nil || !strings.Contains(*res.Error, "unknown session nope") {
		t.Fatalf("result = %+v, want unknown-session error", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("nothing should be typed for an unknown session, got %q", inputs)
	}
}

func TestSessionAnnotationsSubmitRejectsEmptyText(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "session-1", protocol.SessionStateIdle)

	res := sendAnnotationSubmit(t, d, "session-1", "   \n ")

	if res.Success || res.Status != annotationSubmitStatusError ||
		res.Error == nil || !strings.Contains(*res.Error, "text is required") {
		t.Fatalf("result = %+v, want text-required error", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("nothing should be typed for an empty payload, got %q", inputs)
	}
}

func TestSessionAnnotationsSubmitDeliveryFailure(t *testing.T) {
	d := newSubmitDaemon(t)
	d.ptyBackend = &failingInputBackend{fakeSpawnBackend: &fakeSpawnBackend{}}
	addIdleNotebookSession(d, "session-1", protocol.SessionStateIdle)

	res := sendAnnotationSubmit(t, d, "session-1", submitAnnotationText)

	if res.Success || res.Status != annotationSubmitStatusError || res.Error == nil {
		t.Fatalf("result = %+v, want delivery error", res)
	}
}
