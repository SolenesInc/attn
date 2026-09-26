package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHandbackPresentationRoundHeldOffByTypingLandsAfterTheQuietWindow(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, sessionID, inputs := delegateForNotify(t, d, "codex")
	d.store.UpdateState(sessionID, protocol.StateWaitingInput)
	pres, err := d.store.CreatePresentation(sessionID, nil, "Review", "changes", t.TempDir(), time.Now())
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}
	handedBack := func() bool {
		for _, input := range inputs(sessionID) {
			if strings.Contains(input, agentMailboxDoorbellText) {
				return true
			}
		}
		return false
	}
	quiesceTranscriptWatchers(t, d)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		d.handbackPresentationRound(pres, 1, "approved")
		if handedBack() {
			t.Fatalf("typed into a composer the user just used: %q", inputs(sessionID))
		}

		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		if !handedBack() {
			t.Fatalf("nothing resent the handback once the composer went quiet: %q", inputs(sessionID))
		}
	})
}
