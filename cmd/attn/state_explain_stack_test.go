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
	outcomes := map[string]bool{}
	for _, obs := range result.Observations {
		outcomes[obs.Outcome] = true
	}
	if !outcomes["applied"] || !outcomes["skipped"] {
		t.Errorf("one turn recorded outcomes %v, want applied and skipped claims", outcomes)
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
	for _, obs := range result.Observations {
		want := []string{obs.Source, obs.Outcome}
		if reason := strings.TrimSpace(protocol.Deref(obs.Reason)); reason != "" {
			want = append(want, reason)
		}
		if cause := strings.TrimSpace(protocol.Deref(obs.Cause)); cause != "" {
			want = append(want, "cause="+cause)
		}
		if detail := strings.TrimSpace(protocol.Deref(obs.Detail)); detail != "" {
			want = append(want, fmt.Sprintf("%q", detail))
		}
		if !slices.ContainsFunc(rows, func(row string) bool {
			return !slices.ContainsFunc(want, func(field string) bool { return !strings.Contains(row, field) })
		}) {
			t.Errorf("state explain has no row showing %q:\n%s", want, text)
		}
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
