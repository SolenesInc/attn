package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATranscriptAcknowledgementCountsUsageBeforeTheWatcherStarts(t *testing.T) {
	s := testworld.NewStack(t)
	captured := s.PauseAt(pausepoint.DriverTranscriptCaptured)
	applied := s.PauseAt(pausepoint.DriverTranscriptApplied)
	s.Start()
	app := s.App()
	driver := dialDriverAt(t, s.DialUnix, "attn-pi")
	driver.register("pi", map[string]bool{"state_reporting": true, "resume": true})
	awaitDriverAvailable(app, "pi")
	session := s.Spawn(app, fakeagent.Pi, s.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr("existing-conversation")
	})
	run := driver.launched()
	path := s.Path("pi-session.jsonl")
	reported := make(chan error, 1)
	go func() { reported <- reportTranscript(driver, run.SessionID, run.RunID, path) }()
	captured.Await()
	updated := make(chan error, 1)
	go func() {
		updated <- s.Client().UpdateState(protocol.TerminalID(run.SessionID), protocol.StateWaitingInput)
	}()
	applied.Await()
	captured.Release()
	if err := <-reported; err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, path, piTranscriptLines(t))
	applied.Release()
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	stopDriven(app, driver, session)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.Usage != nil && usageRow(s.Usage, "agent") != nil && usageRow(s.Usage, "agent").InputTokens == 17386
	})
	got := showSession(t, s.Client(), session).Usage
	if got == nil || usageRow(got, "agent") == nil || usageRow(got, "agent").InputTokens != 17386 {
		t.Fatalf("usage written after the transcript acknowledgement = %+v, want all 17386 agent input tokens", got)
	}
}
