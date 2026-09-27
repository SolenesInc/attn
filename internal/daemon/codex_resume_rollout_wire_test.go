package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestACodexConversationResumesOnlyFromARolloutWhoseMetadataNamesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	native := "019a0000-0000-7000-8000-00000000abcd"
	rollout := func(day, id string) {
		path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", day, "rollout-2026-07-17T10-00-00-"+native+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		line := `{"timestamp":"2026-07-17T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"/work/shop","source":"cli"}}` + "\n"
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resume := func() *fakeagent.Run {
		session := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.ResumePicker = protocol.Ptr(true)
			m.ResumeSessionID = protocol.Ptr(native)
		})
		return w.Launched(session)
	}

	rollout(filepath.Join("2026", "07", "17"), "019a0000-0000-7000-8000-0000000000ff")
	if run := resume(); run.Resumed || !run.ResumePicker {
		t.Errorf("codex ran as %q from a rollout named for %s that belongs to another conversation, want the resume picker", run.Argv, native)
	}
	rollout(filepath.Join("2026", "07", "18"), native)
	if run := resume(); !run.Resumed || run.ConversationID != native {
		t.Errorf("codex ran as %q, want it resuming %s from the rollout that names it", run.Argv, native)
	}
}
