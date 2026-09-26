package main_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestStateExplainReplaysEachClaimAndWhatBecameOfIt(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))

	for _, args := range [][]string{
		{},
		{"--json", "abc"},
		{"abc", "def"},
		{"abc", "--nope"},
	} {
		r := s.Attn(append([]string{"state", "explain"}, args...)...)
		if r.Code != 2 || !strings.Contains(r.Stderr, "usage: attn state") {
			t.Errorf("state explain %q exited %d: %s", args, r.Code, r.Stderr)
		}
	}

	s.Start()
	if r := s.Attn("state", "explain", "no-such-session"); r.Code != 1 || !strings.Contains(r.Stderr, "state explain:") {
		t.Errorf("state explain of an unknown session exited %d: %s", r.Code, r.Stderr)
	}

	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(id)
	turn := func(state protocol.SessionState) {
		t.Helper()
		app.TypeLine(id, "next")
		claude.Prompted()
		claude.Reply(fmt.Sprintf("done <!-- attn:state=%s -->", state))
		testworld.AwaitSession(app, id, func(x protocol.Session) bool { return x.State == state })
	}
	turn(protocol.SessionStateWaitingInput)

	var result protocol.StateExplainResult
	s.Attn("state", "explain", id, "--json").JSON(t, &result)
	if result.SessionID != id || result.Agent != "claude" || result.State != string(protocol.SessionStateWaitingInput) || result.Capacity == 0 {
		t.Fatalf("state explain --json = %+v", result)
	}
	if !slices.ContainsFunc(result.Observations, func(obs protocol.StateExplainEntry) bool { return obs.Outcome == "applied" }) {
		t.Errorf("one turn recorded %+v, want the claim that moved the session applied", result.Observations)
	}

	text := s.Attn("state", "explain", id).Stdout
	for _, want := range []string{
		fmt.Sprintf("session %s (claude)\n", id),
		"state:   waiting_input since ",
		"model request: ",
		"OBSERVED",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("state explain does not show %q:\n%s", want, text)
		}
	}
	rows := strings.Split(text, "\n")
	next := 0
	for _, obs := range result.Observations {
		claim := obs.Claim
		if claim == "" {
			claim = "-"
		}
		var why []string
		if reason := strings.TrimSpace(protocol.Deref(obs.Reason)); reason != "" {
			why = append(why, reason)
		}
		if cause := strings.TrimSpace(protocol.Deref(obs.Cause)); cause != "" {
			why = append(why, "cause="+cause)
		}
		if detail := strings.TrimSpace(protocol.Deref(obs.Detail)); detail != "" {
			why = append(why, fmt.Sprintf("%q", detail))
		}
		at := slices.IndexFunc(rows[next:], func(row string) bool {
			columns := strings.Fields(row)
			return len(columns) >= 4 && columns[1] == obs.Source && columns[2] == claim && columns[3] == obs.Outcome &&
				!slices.ContainsFunc(why, func(part string) bool { return !strings.Contains(row, part) })
		})
		if at < 0 {
			t.Errorf("state explain has no row after line %d showing %s %s %s %q:\n%s", next, obs.Source, claim, obs.Outcome, why, text)
			continue
		}
		next += at + 1
	}
	if strings.Contains(text, "evicted") {
		t.Errorf("a trace short of capacity claims evictions:\n%s", text)
	}

	for i := 0; ; i++ {
		if i == result.Capacity {
			t.Fatalf("%d turns did not fill a trace of capacity %d", i, result.Capacity)
		}
		turn([]protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateWaitingInput}[i%2])
		var now protocol.StateExplainResult
		s.Attn("state", "explain", id, "--json").JSON(t, &now)
		if len(now.Observations) == now.Capacity {
			break
		}
	}
	full := s.Attn("state", "explain", id).Stdout
	if want := fmt.Sprintf("(showing the most recent %d observations; older ones were evicted)", result.Capacity); !strings.Contains(full, want) {
		t.Errorf("a full trace does not say it is a tail:\n%.600s", full)
	}
}
