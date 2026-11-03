package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPullRequestRecordedWhileTheSessionsConversationIsAppliedIsListedOnceAcknowledged(t *testing.T) {
	s := testworld.NewStack(t)
	applying := s.PauseAt(pausepoint.ConversationReportReady)
	awaited := s.PauseAt(pausepoint.ConversationApplyAwaited)
	s.Start()
	app, cli := s.App(), s.Client()
	driver := dialDriverAt(t, s.DialUnix, "snipe-plugin")
	driver.register("snipe", map[string]bool{"resume": true, "state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	session := s.Spawn(app, scriptedAgent, s.Path("shop"))
	run := driver.launched()
	reported := make(chan error, 1)
	go func() {
		reported <- driver.report("session.report_metadata", map[string]any{
			"session_id": run.SessionID, "run_id": run.RunID, "seq": 1,
			"metadata": map[string]string{"native_id": "first"}, "resume_session_id": "first",
		})
	}()
	applying.Await()
	release, err := recordDuringConversationApply(cli.RecordPullRequestCreated, session, applying, awaited)
	if err != nil {
		t.Fatalf("record while the session's conversation was applied: %v", err)
	}
	if got := pullNumbers(queriedSession(t, cli, session)); !slices.Equal(got, []int{4}) {
		t.Fatalf("pull requests right after the acknowledgement = %v, want [4]", got)
	}
	release()
	if err := <-reported; err != nil {
		t.Fatalf("conversation report: %v", err)
	}
}

func TestAPullRequestRecordedWhileAClearIsAppliedIsNotAcknowledgedForTheClosedSession(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	deciding := s.PauseAt(pausepoint.ConversationDecision)
	awaited := s.PauseAt(pausepoint.ConversationApplyAwaited)
	s.Start()
	app, cli := s.App(), s.Client()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "/clear")
	agent.Prompted()
	deciding.Await()
	release, err := recordDuringConversationApply(cli.RecordPullRequestCreated, first, deciding, awaited)
	release()
	successor := awaitSuccessor(app, first)
	if err == nil {
		t.Fatalf("record for %s was acknowledged, but /clear closed it; its successor %s lists %v", first, successor.ID, pullNumbers(queriedSession(t, cli, string(successor.ID))))
	}
}

func recordDuringConversationApply(record func(protocol.SessionID, string) error, session string, applying, awaited *testworld.Pause) (releaseApply func(), err error) {
	recorded := make(chan error, 1)
	returned := make(chan struct{})
	go func() {
		recorded <- record(protocol.SessionID(session), shopPull(4))
		close(returned)
	}()
	if !awaited.AwaitBefore(returned) {
		return applying.Release, <-recorded
	}
	applying.Release()
	awaited.Release()
	return func() {}, <-recorded
}
