package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACaptureWaitsForTheFolderTrustAnswerAcrossADaemonRestart(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.TrustedApp()
	cli := s.Client()
	setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
	s.AskFolderTrustAtNextBoot()
	id := string(wakeCrew(t, cli, "chief", "").SessionID)
	app.AwaitScreen(id, folderTrustChoice)
	s.Advance(10 * time.Second)
	atQuestion := func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.StateReason) == "folder_trust_question"
	}
	testworld.AwaitSession(app, id, atQuestion)

	s.Stop()
	s.Start()
	app = s.TrustedApp()
	s.AwaitTimer(daemon.ClaudeModReachTimer)
	s.Advance(5 * time.Second)
	if after := queriedSession(t, s.Client(), id); !atQuestion(after) {
		t.Fatalf("after restart the session reads %s/%s, want it still at the folder-trust question", after.State, protocol.Deref(after.StateReason))
	}

	capture := sendQuickCapture(t, app, toTheChief(), "Please investigate the build")
	if record := quickCaptureRecord(t, app, capture.CaptureID); record.ReadAt != nil {
		t.Fatalf("the capture was read at %s while only the trust question was on screen", *record.ReadAt)
	}

	app.TypeLine(id, "\x1b[B")

	run := s.Launched(id)
	run.Prompted()
	run.Reply("Up to speed. <!-- attn:state=waiting_input -->")
	if prompted := run.Prompted(); !strings.Contains(prompted, "Please investigate the build") {
		t.Fatalf("once trusted the agent was prompted %q, want the capture", prompted)
	}
	testworld.Await(app, protocol.EventQuickCaptureRead, func(x protocol.QuickCaptureReadMessage) bool { return x.CaptureID == capture.CaptureID })
}

func TestAStartedClaudeShowingTheTrustChoiceStaysAnOrdinaryQuestionAcrossADaemonRestart(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("Quote the trust menu.")
	})
	run := s.Launched(id)
	run.Prompted()
	run.Reply(folderTrustChoice + " <!-- attn:state=waiting_input -->")
	app.AwaitScreen(id, folderTrustChoice)
	testworld.AwaitSession(app, id, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })

	s.Stop()
	s.Start()

	if after := queriedSession(t, s.Client(), id); protocol.Deref(after.StateReason) == "folder_trust_question" {
		t.Fatalf("a started agent quoting the choice reads %s/%s after restart, want it not held at the trust question", after.State, protocol.Deref(after.StateReason))
	}
}
