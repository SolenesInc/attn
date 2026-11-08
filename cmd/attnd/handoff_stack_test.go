package main_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func wakeMember(t *testing.T, s *testworld.Stack, name string) string {
	t.Helper()
	var woken protocol.CrewWakeResult
	s.Attn("crew", "wake", name, "--json").JSON(t, &woken)
	s.Launched(string(woken.SessionID)).Prompted()
	return string(woken.SessionID)
}

func filedLetter(t *testing.T, stdout, member string) string {
	t.Helper()
	_, rest, found := strings.Cut(stdout, member+"'s letter ")
	if !found {
		t.Fatalf("stdout names no letter for %s:\n%s", member, stdout)
	}
	_, path, _ := strings.Cut(rest, " at ")
	path, _, _ = strings.Cut(path, ".\n")
	return path
}

func TestAHandoffFilesTheLetterAndTurnsTheDayOver(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	for _, row := range []struct {
		args []string
		want []string
	}{
		{[]string{"handoff"}, []string{"-m -"}},
		{[]string{"handoff", "--day", "removed", "-m", "letter"}, []string{"flag provided but not defined: -day"}},
		{[]string{"handoff", "-m", "   "}, []string{"-m -"}},
		{[]string{"handoff", "--retry", "-m", "another one"}, []string{"--retry"}},
		{[]string{"handoff", "-m", "x", "trellis"}, []string{`"trellis"`}},
		{[]string{"handoff", "-m", "x", "--sleep"}, []string{"flag provided but not defined: -sleep"}},
		{[]string{"handoff", "-m", "x", "--nap"}, []string{"flag provided but not defined: -nap"}},
	} {
		refused := s.Attn(row.args...)
		if refused.Code != 2 || !strings.HasPrefix(refused.Stderr, "handoff: ") {
			t.Errorf("attn %q exited %d with stderr %q, want a refusal", row.args, refused.Code, refused.Stderr)
		}
		requireLines(t, strings.Join(row.args, " "), refused.Stderr, row.want...)
	}

	writeCharter(t, s, "keel")
	writeCharter(t, s, "trellis")
	s.Start()
	keel := wakeMember(t, s, "keel")
	letter := "Dear next keel,\nthe join test is the gate.\n"
	slept := s.Run(testworld.Invocation{Args: []string{"handoff", "-m", "-"}, Session: keel, Stdin: letter})
	requireStdout(t, slept, "Keel's letter is filed at ", "Keel is asleep.")
	if filed, err := os.ReadFile(filedLetter(t, slept.Stdout, "Keel")); err != nil || string(filed) != letter {
		t.Fatalf("the filed letter reads %q (%v), want %q", filed, err, letter)
	}
	if day := crewRoster(t, s)["keel"].BindingSession; day != nil {
		t.Fatalf("keel slept but is still bound to %s", *day)
	}

	workdir := s.Path("trellis")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	requireStdout(t, s.Attn("crew", "set", "trellis", "--cwd", workdir), "Trellis launches in "+workdir)
	trellis := wakeMember(t, s, "trellis")
	requireStdout(t, s.Attn("crew", "set", "trellis", "--agent", "codex"))
	requireStdout(t, s.Attn("crew", "apply", "trellis"), "Trellis's session "+trellis[:8]+" is busy; it will be asked to file its letter when it can take input, and its next session starts on codex from it.")
	if err := os.Remove(workdir); err != nil {
		t.Fatal(err)
	}
	stuck := s.Run(testworld.Invocation{Args: []string{"handoff", "-m", "Dear next trellis,", "--session", trellis}, Session: keel})
	if stuck.Code != 1 {
		t.Fatalf("a handoff whose successor could not start exited %d: %s", stuck.Code, stuck.Stderr)
	}
	requireLines(t, "stdout", stuck.Stdout, "Trellis's letter is filed at ")
	requireLines(t, "stderr", stuck.Stderr, "handoff: the next session did not start: ", "`attn handoff --retry`")
	path := filedLetter(t, stuck.Stdout, "Trellis")
	if day := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession); string(day) != trellis {
		t.Fatalf("after the failed successor trellis is bound to %q, want its day %s", day, trellis)
	}

	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	retried := s.Run(testworld.Invocation{Args: []string{"handoff", "--retry"}, Session: trellis})
	requireStdout(t, retried, "Trellis's letter was already filed at "+path+".\n", "Trellis's next session is ")
	next := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession)
	if next == "" || string(next) == trellis || !strings.Contains(retried.Stdout, string(next[:8]+" on its new harness")) {
		t.Fatalf("the retry started %q:\n%s", next, retried.Stdout)
	}
	if run := s.Launched(string(next)); run.Harness != fakeagent.Codex {
		t.Fatalf("trellis's next session runs %s, want codex", run.Harness)
	}
	if filed, err := os.ReadFile(path); err != nil || string(filed) != "Dear next trellis,\n" {
		t.Fatalf("the retried letter reads %q (%v), want the one letter filed before", filed, err)
	}
}

func TestCrewApplyReloadsAModelChangeInPlace(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "keel")
	s.Start()
	requireStdout(t, s.Attn("crew", "apply", "keel"), "Keel is already asleep; its next wake uses the saved settings.")
	var woken protocol.CrewWakeResult
	s.Attn("crew", "wake", "keel", "--json").JSON(t, &woken)
	keel := string(woken.SessionID)
	first := s.Launched(keel)
	first.Prompted()

	requireStdout(t, s.Attn("crew", "set", "keel", "--model", "claude-sonnet-fake"))
	requireStdout(t, s.Attn("crew", "apply", "keel"), "Reloaded Keel's session "+keel[:8]+" in place on claude with its saved settings; its conversation continues.")
	again := s.Launched(keel)
	model := ""
	if i := slices.Index(again.Argv, "--model"); i >= 0 && i+1 < len(again.Argv) {
		model = again.Argv[i+1]
	}
	if !again.Resumed || again.ConversationID != first.ConversationID || model != "claude-sonnet-fake" {
		t.Fatalf("the reload ran resumed=%v conversation=%s model=%q, want %s kept on claude-sonnet-fake", again.Resumed, again.ConversationID, model, first.ConversationID)
	}
	if day := protocol.Deref(crewRoster(t, s)["keel"].BindingSession); string(day) != keel {
		t.Fatalf("after the reload keel is bound to %q, want %s", day, keel)
	}
}
