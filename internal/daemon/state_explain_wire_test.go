package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestStateExplainAttributesEachHookClaimToItsSourceAndCollapsesRepeats(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		if err := cli.RegisterWithAgent("s1", "s1", w.Path("s1"), string(protocol.SessionAgentClaude)); err != nil {
			t.Fatalf("register s1: %v", err)
		}
		if err := cli.RegisterWithAgent("s2", "s2", w.Path("s2"), string(protocol.SessionAgentCodex)); err != nil {
			t.Fatalf("register s2: %v", err)
		}

		reported := time.Now()
		if err := cli.UpdateStateFromHook("s1", protocol.StateWorking, "auto"); err != nil {
			t.Fatalf("report s1 working: %v", err)
		}
		working := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		w.advance(time.Second)
		notified := time.Now()
		if err := cli.RecordNotification("s1", "permission_prompt", "Claude needs your permission"); err != nil {
			t.Fatalf("notify s1: %v", err)
		}
		w.advance(time.Second)

		explained := stateExplainOf(t, cli, "s1")
		if explained.SessionID != "s1" || explained.Agent != "claude" || explained.State != string(protocol.SessionStateWorking) ||
			protocol.Deref(explained.StateSince) != working.StateSince || explained.Capacity == 0 {
			t.Fatalf("explain names %s (%s) %s since %s with capacity %d, want s1 (claude) working since %s with a capacity",
				explained.SessionID, explained.Agent, explained.State, protocol.Deref(explained.StateSince), explained.Capacity, working.StateSince)
		}
		stateExplainShows(t, explained.Observations,
			stateExplainRow{source: "reviewer", claim: "auto", outcome: "observed", at: reported},
			stateExplainRow{source: "hook_state", claim: "working", outcome: "observed", at: reported},
			stateExplainRow{source: "resolver", claim: "working", outcome: "applied", cause: "resolver_observation", detail: "bracket_open", at: reported},
			stateExplainRow{source: "hook_notify", claim: "permission_prompt", outcome: "observed", detail: "Claude needs your permission", at: notified},
		)

		first := time.Now()
		for range 3 {
			if err := cli.UpdateState("s2", protocol.StateWorking); err != nil {
				t.Fatalf("report s2 working: %v", err)
			}
			w.advance(time.Second)
		}
		codex := stateExplainOf(t, cli, "s2").Observations
		stateExplainShows(t, codex,
			stateExplainRow{source: "hook_state", claim: "working", outcome: "observed", at: first},
			stateExplainRow{source: "resolver", claim: "working", outcome: "applied", cause: "resolver_observation", detail: "bracket_open", at: first},
			stateExplainRow{source: "hook_state", claim: "working", outcome: "observed", repeats: 1, at: first.Add(2 * time.Second)},
		)
		for _, obs := range codex {
			if obs.Source == "reviewer" {
				t.Fatalf("a hook without a permission mode was explained with a reviewer claim %q", obs.Claim)
			}
		}
	})
}

func TestStateExplainFollowsASessionThroughItsTurnARestartAndItsClose(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	spawned, workspaceID, paneID := w.RequestSpawn(app, fakeagent.Claude, w.Path("shop"))
	session := spawned.ID
	run := w.Launched(session)
	app.TypeLine(session, "rename the checkout module")
	run.Prompted()
	run.ReplyAfterStop("Keep the old import path as an alias? <!-- attn:state=waiting_input -->")
	waiting := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	explained := stateExplainOf(t, cli, session)
	if explained.SessionID != session || explained.Agent != "claude" || explained.State != string(protocol.SessionStateWaitingInput) ||
		protocol.Deref(explained.StateSince) != waiting.StateSince {
		t.Fatalf("explain names %s (%s) %s since %s, want %s (claude) waiting_input since %s",
			explained.SessionID, explained.Agent, explained.State, protocol.Deref(explained.StateSince), session, waiting.StateSince)
	}
	stateExplainShows(t, explained.Observations,
		stateExplainRow{source: "heartbeat", claim: "busy", outcome: "observed", detail: "Claude Code"},
	)
	stateExplainShows(t, explained.Observations,
		stateExplainRow{source: "hook_state", claim: "working", outcome: "observed"},
		stateExplainRow{source: "resolver", outcome: "skipped", reason: "awaiting_verdict", repeats: -1},
		stateExplainRow{source: "classifier", claim: "waiting_input", outcome: "observed", detail: "classifier"},
		stateExplainRow{source: "resolver", claim: "waiting_input", outcome: "applied", cause: "resolver_observation", detail: "classifier_verdict"},
	)

	app.TypeLine(session, "yes, keep it")
	run.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	w.restart()
	app = w.App()
	recovered := stateExplainOf(t, cli, session)
	if recovered.State != string(protocol.SessionStateRecoverable) {
		t.Fatalf("after the restart explain shows the session %s, want recoverable", recovered.State)
	}
	stateExplainShows(t, recovered.Observations,
		stateExplainRow{source: "startup_recovery", claim: "recoverable", outcome: "applied", cause: "startup_recovery"},
	)

	closed := testworld.Request(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	}, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
		return r.Action == protocol.CmdWorkspaceLayoutClosePane && protocol.Deref(r.PaneID) == paneID
	})
	if !closed.Success {
		t.Fatalf("close the session's pane: %s", protocol.Deref(closed.Error))
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && e.Session.ID == session })
	if _, err := cli.StateExplain(session); err == nil || !strings.Contains(err.Error(), "session_not_found") {
		t.Fatalf("explain of a closed session answered %v, want session_not_found", err)
	}
}

type stateExplainRow struct {
	source, claim, outcome, cause, reason, detail string
	repeats                                       int
	at                                            time.Time
}

func (r stateExplainRow) matches(t *testing.T, obs protocol.StateExplainEntry) bool {
	t.Helper()
	for _, stamp := range []string{obs.ObservedAt, obs.RecordedAt} {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			t.Fatalf("explain entry %+v carries a time that is not RFC3339: %v", obs, err)
		}
	}
	observed, _ := time.Parse(time.RFC3339Nano, obs.ObservedAt)
	return obs.Source == r.source && obs.Claim == r.claim && obs.Outcome == r.outcome &&
		protocol.Deref(obs.Cause) == r.cause && protocol.Deref(obs.Reason) == r.reason &&
		protocol.Deref(obs.Detail) == r.detail && (r.repeats < 0 || protocol.Deref(obs.Repeats) == r.repeats) &&
		(r.at.IsZero() || observed.Equal(r.at))
}

func stateExplainShows(t *testing.T, observations []protocol.StateExplainEntry, want ...stateExplainRow) {
	t.Helper()
	next := 0
	for _, row := range want {
		found := false
		for ; next < len(observations) && !found; next++ {
			found = row.matches(t, observations[next])
		}
		if !found {
			t.Fatalf("explain does not show %+v in order; it shows:\n%s", row, describeStateExplain(observations))
		}
	}
}

func describeStateExplain(observations []protocol.StateExplainEntry) string {
	var rows []string
	for _, obs := range observations {
		rows = append(rows, strings.Join([]string{
			obs.Source, obs.Claim, obs.Outcome, "cause=" + protocol.Deref(obs.Cause), "reason=" + protocol.Deref(obs.Reason),
			"detail=" + protocol.Deref(obs.Detail), "observed_at=" + obs.ObservedAt,
		}, " "))
	}
	return strings.Join(rows, "\n")
}

func stateExplainOf(t *testing.T, cli *client.Client, session string) *protocol.StateExplainResult {
	t.Helper()
	explained, err := cli.StateExplain(session)
	if err != nil {
		t.Fatalf("state explain %s: %v", session, err)
	}
	return explained
}
