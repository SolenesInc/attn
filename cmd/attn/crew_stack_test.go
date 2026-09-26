package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func crewRoster(t *testing.T, s *testworld.Stack) map[string]protocol.CrewMember {
	t.Helper()
	var members []protocol.CrewMember
	s.Attn("crew", "list", "--json").JSON(t, &members)
	roster := map[string]protocol.CrewMember{}
	for _, member := range members {
		roster[member.ID] = member
	}
	return roster
}

func crewRow(t *testing.T, table, name string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, name+" ") {
			return line
		}
	}
	t.Fatalf("no row for %s:\n%s", name, table)
	return ""
}

func requireStdout(t *testing.T, got testworld.Result, want ...string) {
	t.Helper()
	if got.Code != 0 {
		t.Fatalf("exited %d: %s", got.Code, got.Stderr)
	}
	requireLines(t, "stdout", got.Stdout, want...)
}

func writeCharter(t *testing.T, s *testworld.Stack, name string) {
	t.Helper()
	home := filepath.Join(s.Dir, "crew", name)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "CHARTER.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCrewMembersWakeSleepAndKeepTheirLaunchSettings(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	requireRefusals(t, s, []argvRefusal{
		{args: []string{"crew", "list", "extra"}, want: "takes no arguments"},
		{args: []string{"crew", "list", "--nope"}, want: "not defined: -nope"},
		{args: []string{"crew", "wake"}, want: "takes one member name"},
		{args: []string{"crew", "wake", "one", "two"}, want: `takes one member name, not "two"`},
		{args: []string{"crew", "wake", "--nope", "keel"}, want: "not defined: -nope"},
		{args: []string{"crew", "sleep"}, want: "takes one member name"},
		{args: []string{"crew", "sleep", "one", "two"}, want: `takes one member name, not "two"`},
		{args: []string{"crew", "sleep", "keel", "--request-id", "retry-1"}, want: "not defined: -request-id"},
		{args: []string{"crew", "restart", "keel", "--request-id", ""}, want: "--request-id cannot be empty"},
		{args: []string{"crew", "restart", "keel", "--request-id", "   "}, want: "--request-id cannot be empty"},
		{args: []string{"crew", "set", "keel"}, want: "nothing to set"},
	})

	s.Start()
	requireStdout(t, s.Attn("crew", "list"), "No crew members are registered", "<name>/CHARTER.md")
	s.Stop()
	for _, name := range []string{"keel", "trellis"} {
		writeCharter(t, s, name)
	}
	s.Start()
	app := s.App()

	notes, specs := s.Path("notes"), s.Path("specs")
	for _, dir := range []string{notes, specs} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	requireStdout(t, s.Attn("crew", "set", "keel", "--agent", "codex", "--effort", "high", "--awareness-dir", notes, "--awareness-dir", specs),
		"Keel launches in - on codex, model ", ", effort high\n", "awareness dirs: "+notes+", "+specs+"\n")
	roster := crewRoster(t, s)
	if keel := roster["keel"]; keel.ResolvedAgent != "codex" || protocol.Deref(keel.ResolvedEffort) != "high" || !slices.Equal(keel.AwarenessDirs, []string{notes, specs}) {
		t.Fatalf("keel after crew set = %+v", keel)
	}
	if trellis := roster["trellis"]; trellis.ResolvedAgent != "claude" || trellis.Agent != nil || trellis.Model != nil || trellis.Effort != nil || len(trellis.AwarenessDirs) != 0 {
		t.Fatalf("setting keel touched trellis: %+v", trellis)
	}
	table := s.Attn("crew", "list").Stdout
	requireLines(t, "crew list", table, "MEMBER", "STATE", "AGENT", "MODEL", "EFFORT", "SESSION", "HOME")
	requireLines(t, "keel's row", crewRow(t, table, "Keel"), " asleep ", " codex ", " high ", filepath.Join(s.Dir, "crew", "keel"))
	requireLines(t, "trellis's row", crewRow(t, table, "Trellis"), " asleep ", " claude ", " "+protocol.Deref(roster["trellis"].ResolvedModel)+" ", filepath.Join(s.Dir, "crew", "trellis"))

	requireStdout(t, s.Attn("crew", "set", "keel", "--agent", "", "--effort", "", "--awareness-dir", ""), "Keel launches in - on claude", "awareness dirs: -\n")
	if keel := crewRoster(t, s)["keel"]; keel.Agent != nil || keel.Model != nil || keel.Effort != nil || keel.ResolvedAgent != "claude" || len(keel.AwarenessDirs) != 0 {
		t.Fatalf("keel after clearing its settings = %+v", keel)
	}

	requireStdout(t, s.Attn("crew", "wake", "trellis"), "Trellis is awake in session ")
	day := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession)
	trellis := s.Launched(day)
	trellis.Prompted()
	requireLines(t, "trellis's row", crewRow(t, s.Attn("crew", "list").Stdout, "Trellis"), " awake ", " "+day[:8]+" ")
	agents := s.Attn("agent", "list").Stdout
	requireLines(t, "agent list", agents, "MEMBER", "An ID or awake MEMBER here works with `attn agent peek <target>`")
	requireLines(t, "trellis's agent row", crewRow(t, agents, day[:8]), " Trellis ")
	requireStdout(t, s.Attn("agent", "peek", "trellis"), "session "+day, "crew member: this session is Trellis today")
	requireFailure(t, s.Attn("agent", "peek", "keel"), "agent peek: ", "Keel is asleep", "never wakes", "`attn crew wake keel`")
	requireStdout(t, s.Attn("crew", "wake", "trellis"), "Trellis is already awake in session "+day[:8]+" — nothing was launched.")

	trellis.Exit(0)
	testworld.AwaitSession(app, day, func(x protocol.Session) bool { return protocol.Deref(x.StateReason) == "process_exited" })
	requireStdout(t, s.Attn("crew", "wake", "trellis"), "Previous session "+day[:8]+" had exited; its binding was released.\n", "Trellis is awake in session ")
	next := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession)
	if next == day || next == "" {
		t.Fatalf("trellis woke into %q after %q exited", next, day)
	}
	s.Launched(next).Prompted()
	requireStdout(t, s.Attn("crew", "sleep", "trellis"), "Sleep request for Trellis is queued in session "+next[:8]+" — ")
	requireStdout(t, s.Attn("crew", "sleep", "keel"), "Keel is already asleep", "no sleep request was sent")

	var woken protocol.CrewWakeResult
	s.Attn("crew", "wake", "keel", "--agent", "codex", "--json").JSON(t, &woken)
	if woken.Member != "keel" || woken.AlreadyAwake || woken.SessionID == "" {
		t.Fatalf("crew wake keel --agent codex --json = %+v", woken)
	}
	if codex := s.Launched(woken.SessionID); codex.Harness != fakeagent.Codex {
		t.Fatalf("keel woke on %s, want codex", codex.Harness)
	} else {
		codex.Exit(0)
	}
	testworld.AwaitSession(app, woken.SessionID, func(x protocol.Session) bool { return protocol.Deref(x.StateReason) == "process_exited" })

	restarts := make([]protocol.CrewRestartResult, 2)
	for i := range restarts {
		restarted := s.Attn("crew", "restart", "keel", "--request-id", " retry-1 ", "--json")
		var receipt struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal([]byte(restarted.Stderr), &receipt); restarted.Code != 0 || err != nil || receipt.RequestID != "retry-1" {
			t.Fatalf("restart exited %d with stderr %q, want the JSON receipt for retry-1", restarted.Code, restarted.Stderr)
		}
		restarted.JSON(t, &restarts[i])
	}
	first, retried := restarts[0].Restart, restarts[1].Restart
	successor := protocol.Deref(first.SuccessorSessionID)
	if first.RequestID != "retry-1" || first.State != protocol.CrewRestartStateCompleted || successor == "" ||
		retried.RequestID != first.RequestID || retried.State != first.State || protocol.Deref(retried.SuccessorSessionID) != successor {
		t.Fatalf("a retried restart = %+v then %+v, want the same completed request", first, retried)
	}
	var sessions []struct {
		ID     string `json:"id"`
		Member string `json:"member"`
	}
	s.Attn("agent", "list", "--json").JSON(t, &sessions)
	var days []string
	for _, session := range sessions {
		if session.Member == "keel" {
			days = append(days, session.ID)
		}
	}
	if !slices.Equal(days, []string{successor}) {
		t.Fatalf("a retried restart left keel in sessions %q, want only %s", days, successor)
	}
	keel := s.Launched(successor)
	keel.Prompted()
	keel.Reply("Read the charter. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, successor, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	requireStdout(t, s.Attn("crew", "sleep", "keel"), "Asked Keel in session "+successor[:8]+" to write its handoff and file it with `attn handoff --sleep`.")

	unknown := s.Attn("crew", "restart", "nobody")
	receipt, _, _ := strings.Cut(unknown.Stderr, "\n")
	if requestID, printed := strings.CutPrefix(receipt, "crew restart request: request_id="); unknown.Code != 1 || !printed || strings.TrimSpace(requestID) == "" {
		t.Fatalf("a restart without --request-id exited %d with stderr %q, want a generated receipt first", unknown.Code, unknown.Stderr)
	}
}
