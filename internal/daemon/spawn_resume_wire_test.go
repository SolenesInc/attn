package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClaudeRespawnResumesOnlyAConversationThatStillExists(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	first := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	first.Prompted()
	first.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	resumed := respawn(w, app, fakeagent.Claude, session, cwd)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("respawn ran claude %q, want it resuming %s", resumed.Argv, first.ConversationID)
	}

	transcripts, err := filepath.Glob(filepath.Join(w.Dir, "toolhome", ".claude", "projects", "*", first.ConversationID+".jsonl"))
	if err != nil || len(transcripts) != 1 {
		t.Fatalf("transcript of %s = %v (%v), want exactly one", first.ConversationID, transcripts, err)
	}
	if err := os.Remove(transcripts[0]); err != nil {
		t.Fatal(err)
	}
	fresh := respawn(w, app, fakeagent.Claude, session, cwd)
	if fresh.Resumed {
		t.Errorf("respawn after the transcript was deleted ran claude %q, want a fresh conversation", fresh.Argv)
	}
}
