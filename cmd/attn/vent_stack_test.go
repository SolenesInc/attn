package main_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var ventRecorded = regexp.MustCompile(`^vent (v-[0-9a-z]{6}) recorded\. Keep working; nobody replies to vents\.\n$`)

func TestAnAgentVentsOncePerTurnAndWithdrawsWithoutWritingTheOutbox(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	before := s.Spawn(app, fakeagent.Claude, s.Path("before"))
	s.Launched(before)
	turnOnAnalytics(t, app)
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.InitialPrompt = protocol.Ptr("fix the build") })
	agent := s.Launched(session)
	agent.Prompted()
	working := testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	vent := func(id string, args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: append([]string{"vent"}, args...), Session: id})
	}
	recorded := func(got testworld.Result) string {
		t.Helper()
		line := ventRecorded.FindStringSubmatch(got.Stdout)
		if got.Code != 0 || line == nil {
			t.Fatalf("attn vent exited %d with stdout %q and stderr %q", got.Code, got.Stdout, got.Stderr)
		}
		return line[1]
	}

	refused(t, vent(before, "the build is slow"), 1, "vent refused: this session launched while usage analytics was off, so it cannot vent")

	first := recorded(vent(session, "make test skipped the Go suite after I changed internal/store; I ran go test by hand."))
	shown := testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 1 })
	got := shown.Vents[0]
	if got.ID != first || string(got.SessionID) != session || !strings.HasPrefix(got.Text, "make test skipped") {
		t.Fatalf("the app shows %+v, want %s from %s", got, first, session)
	}
	if c := got.Context; c.Harness != "claude" || protocol.Deref(c.HarnessVersion) != fakeagent.ClaudeVersion ||
		!slices.Contains(c.Experimental, "analytics.enabled") || c.Build.Protocol != protocol.ProtocolVersion {
		t.Errorf("the vent's context is %+v, want the harness, its version, the experimental settings at launch and the build", c)
	}

	refused(t, vent(session, "and the linter is slow"), 1,
		"vent refused: the limit is 1 vent per turn, and this turn already has "+first+". Withdraw it (attn vent withdraw "+first+") and send one vent that covers both")
	if withdrawn := vent(session, "withdraw", first); withdrawn.Code != 0 || withdrawn.Stdout != "Withdrew "+first+".\n" {
		t.Fatalf("attn vent withdraw exited %d and printed %q, stderr %q", withdrawn.Code, withdrawn.Stdout, withdrawn.Stderr)
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 1 && m.Vents[0].WithdrawnAt != nil
	})
	refused(t, vent(session, "withdraw", first), 1, "vent withdraw: "+first+" is not a vent this session can withdraw")
	both := recorded(s.Run(testworld.Invocation{Args: []string{"vent"}, Session: session,
		Stdin: "make test skipped the Go suite, and the linter took four minutes.\n"}))

	nextRun := func(reply, prompt string) {
		t.Helper()
		agent.Reply(reply + " <!-- attn:state=waiting_input -->")
		stopped := testworld.AwaitStateAfter(app, working, func(x protocol.Session) bool {
			return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
		})
		app.TypeLine(session, prompt)
		agent.Prompted()
		working = testworld.AwaitStateAfter(app, stopped, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	}
	nextRun("Fixed.", "now the docs")
	second := recorded(vent(session, "attn seed tend refused a seed held by a session that ended; I used --force."))
	nextRun("Docs done.", "and the changelog")
	third := recorded(vent(session, "attn turn settle printed nothing and left my turn open."))
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 4 && m.Vents[0].ID == third && m.Vents[1].ID == second && m.Vents[2].ID == both
	})

	s.Stop()
	s.Start()
	app = s.App()
	recorded(vent(session, "attn daemon restarted under me and my terminal flickered."))
	agent.Reply("Changelog done. <!-- attn:state=waiting_input -->")
	stopped := testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	app.TypeLine(session, "and the release notes")
	agent.Prompted()
	testworld.AwaitStateAfter(app, stopped, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	recorded(vent(session, "attn seed show printed the plot body twice."))
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 6 })

	listed := testworld.Request(app, protocol.ListAnalyticsRecordsMessage{Cmd: protocol.CmdListAnalyticsRecords, RequestID: protocol.Ptr("outbox"), Limit: 1000},
		protocol.EventListAnalyticsRecordsResult, func(r protocol.ListAnalyticsRecordsResultMessage) bool { return r.RequestID == "outbox" })
	if !listed.Success || len(listed.Records) != 0 || listed.Waiting != 0 {
		t.Errorf("the outbox holds %d records (%d waiting, error %q) after six vents and a withdrawal, want none", len(listed.Records), listed.Waiting, protocol.Deref(listed.Error))
	}
}

func turnOnAnalytics(t *testing.T, app *testworld.Peer) {
	t.Helper()
	enabled := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "analytics.enabled", Value: "true", RequestID: protocol.Ptr("analytics")},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "analytics" })
	if !protocol.Deref(enabled.Success) {
		t.Fatalf("turn analytics on: %s", protocol.Deref(enabled.Error))
	}
}
