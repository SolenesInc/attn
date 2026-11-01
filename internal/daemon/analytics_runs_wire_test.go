package daemon_test

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type runRecord struct {
	StartedBy  string `json:"started_by"`
	DurationMs *int64 `json:"duration_ms"`
	Ended      string `json:"ended"`
	OpenedTurn bool   `json:"opened_turn"`
	Approvals  int    `json:"approvals"`
	Usage      string `json:"usage"`
	Tokens     *struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cache_read"`
		CacheWrite int `json:"cache_write"`
	} `json:"tokens"`
	Session analytics.SessionTraits `json:"session"`
}

func (r runRecord) String() string {
	tokens := "none"
	if r.Tokens != nil {
		tokens = fmt.Sprint(r.Tokens.Input + r.Tokens.Output + r.Tokens.CacheRead + r.Tokens.CacheWrite)
	}
	return fmt.Sprintf("started_by=%s ended=%s opened_turn=%v approvals=%d usage=%s tokens=%s harness=%s",
		r.StartedBy, r.Ended, r.OpenedTurn, r.Approvals, r.Usage, tokens, r.Session.Harness)
}

func runRecords(t *testing.T, w *world, app *testworld.Peer, session string) []runRecord {
	t.Helper()
	secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
	if err != nil {
		t.Fatal(err)
	}
	subject := analytics.HashSubject(secret, session)
	var runs []runRecord
	for _, record := range streamOutbox(t, app, analytics.StreamRun) {
		if record.Name != "run" || record.Subject != subject {
			continue
		}
		var run runRecord
		if err := json.Unmarshal(record.Props, &run); err != nil {
			t.Fatalf("run record props %s: %v", record.Props, err)
		}
		if run.DurationMs == nil || *run.DurationMs < 0 {
			t.Errorf("run record %s has no duration", record.Props)
		}
		runs = append(runs, run)
	}
	slices.Reverse(runs)
	return runs
}

func runEnds(w *world, app *testworld.Peer, agent *fakeagent.Run, working protocol.Session, reply string) protocol.Session {
	w.T.Helper()
	agent.Reply(reply)
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	w.readTranscripts()
	return stopped
}

func assertRuns(t *testing.T, got []runRecord, want []string) {
	t.Helper()
	lines := make([]string, len(got))
	for i, run := range got {
		lines[i] = run.String()
	}
	if !slices.Equal(lines, want) {
		t.Errorf("run records:\n  %s\nwant:\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestEachClaudeRunIsOneRunRecordNamingWhatStartedIt(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	setAnalyticsEnabled(app, "true")
	launchReply := "Planned the refund flow. <!-- attn:state=idle -->"
	session, agent, working := mailAgentAtWork(w, app, "shop", "plan the refund flow")
	stopped := runEnds(w, app, agent, working, launchReply)

	settled := settleFromApp(app, stopped)
	working, _ = notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
	ringReply := "Nothing to do. <!-- attn:state=idle -->"
	stopped = runEnds(w, app, agent, working, ringReply)

	app.TypeLine(session, "add a refund button")
	agent.Prompted()
	working = testworld.AwaitStateAfter(app, stopped, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	userReply := "Added it. Ship it? <!-- attn:state=waiting_input -->"
	stopped = runEnds(w, app, agent, working, userReply)

	agent.ProgramStatus("state=working:app=claude-code")
	working = testworld.AwaitStateAfter(app, stopped, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	resumedReply := "The background build passed. <!-- attn:state=idle -->"
	runEnds(w, app, agent, working, resumedReply)

	assertRuns(t, runRecords(t, w, app, session), []string{
		fmt.Sprintf("started_by=launch ended=idle opened_turn=true approvals=0 usage=known tokens=%d harness=claude", claudeTokens(launchReply)),
		fmt.Sprintf("started_by=ring ended=idle opened_turn=false approvals=0 usage=known tokens=%d harness=claude", claudeTokens(ringReply)),
		fmt.Sprintf("started_by=user ended=waiting_input opened_turn=true approvals=0 usage=known tokens=%d harness=claude", claudeTokens(userReply)),
		fmt.Sprintf("started_by=agent ended=idle opened_turn=false approvals=0 usage=known tokens=%d harness=claude", claudeTokens(resumedReply)),
	})
}

func TestACodexRunThroughAnApprovalIsOneRunRecord(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "auto_approve_enabled", "false")
	setAnalyticsEnabled(app, "true")
	session, codex, working := codexAtWork(t, w, app)
	before := queriedSession(t, w.Client(), session).Usage
	owedBefore := protocol.Deref(working.TurnOwed)

	codex.AskApproval()
	asking := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	codex.Dismiss()
	working = testworld.AwaitStateAfter(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	stopped := runEnds(w, app, codex, working, "Ran the migration. <!-- attn:state=idle -->")
	opened := !owedBefore && protocol.Deref(stopped.TurnOwed)

	after := queriedSession(t, w.Client(), session).Usage
	spent := after.TotalTokens
	if before != nil {
		spent -= before.TotalTokens
	}
	assertRuns(t, runRecords(t, w, app, session), []string{
		fmt.Sprintf("started_by=user ended=idle opened_turn=%v approvals=1 usage=known tokens=%d harness=codex", opened, spent),
	})
}

func TestARunWithNoWatchedTranscriptRecordsItsUsageUnavailable(t *testing.T) {
	t.Setenv("ATTN_AGENT_CLAUDE_TRANSCRIPT_WATCHER", "0")
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setAnalyticsEnabled(app, "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	idle := settleFromApp(app, testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle }))
	app.TypeLine(session, "find the flaky test")
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) {
		t.Fatal("the user's run stopped without opening a turn")
	}

	assertRuns(t, runRecords(t, w, app, session), []string{
		"started_by=user ended=idle opened_turn=true approvals=0 usage=unavailable tokens=none harness=claude",
	})
}
