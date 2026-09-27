package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentWithoutUsageReportingShowsNoUsage(t *testing.T) {
	w := newWorld(t, fakeagent.Copilot)
	app := w.App()
	session := w.Spawn(app, fakeagent.Copilot, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	copilot := w.Launched(session)
	copilot.Prompted()
	copilot.Reply("Renamed the checkout module. <!-- attn:state=idle -->")
	settled := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if settled.Usage != nil {
		t.Errorf("a copilot session shows usage %+v, want none", settled.Usage)
	}
}

func TestAResumedConversationCountsOnlyTheUsageAfterTheResume(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	earlier := w.Spawn(app, fakeagent.Claude, cwd)
	first := w.Launched(earlier)
	app.TypeLine(earlier, "find the flaky checkout test")
	first.Prompted()
	oldResearch, oldAnswer := "The flaky test races the tax lookup.", "It races the tax lookup. <!-- attn:state=idle -->"
	first.Subagent(oldResearch)
	first.Reply(oldAnswer)
	awaitUsageTokens(app, earlier, claudeTokens(oldResearch)+claudeTokens(oldAnswer))
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: earlier})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == earlier })

	session := w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(first.ConversationID)
	})
	resumed := w.Launched(session)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("the launch ran claude %q, want it to resume %s", resumed.Argv, first.ConversationID)
	}
	if window := messageWindow(app, session); window.Status != protocol.SessionMessageWindowStatusReady || len(window.Messages) != 1 {
		t.Fatalf("the resumed session shows %s %+v, want the conversation it resumed", window.Status, window.Messages)
	}
	app.TypeLine(session, "fix it")
	resumed.Prompted()
	newResearch, newAnswer := "The lookup needs a lock.", "Fixed with a lock. <!-- attn:state=idle -->"
	resumed.Subagent(newResearch)
	resumed.Reply(newAnswer)
	if usage := awaitUsageTokens(app, session, claudeTokens(newResearch)+claudeTokens(newAnswer)); usage.MeasurementIncomplete != nil {
		t.Errorf("usage of the resumed conversation = %+v, want it complete", usage)
	}
}

func TestAReplacedTranscriptFlagsTheUsageIncomplete(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	claude := w.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	claude.Prompted()
	answer := "It races the tax lookup. <!-- attn:state=idle -->"
	claude.Reply(answer)
	awaitUsageTokens(app, session, claudeTokens(answer))

	transcripts, err := filepath.Glob(filepath.Join(w.Dir, "toolhome", ".claude", "projects", "*", claude.ConversationID+".jsonl"))
	if err != nil || len(transcripts) != 1 {
		t.Fatalf("the conversation's transcript = %q, %v", transcripts, err)
	}
	replacement := `{"type":"summary","summary":"` + strings.Repeat("compacted ", 200) + `"}` + "\n"
	if err := os.WriteFile(transcripts[0], []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.Usage != nil && protocol.Deref(s.Usage.MeasurementIncomplete)
	})
}
