package main_test

import (
	"os"
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
	s.Launched(woken.SessionID).Prompted()
	return woken.SessionID
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

func TestAHandoffFilesTheLetterAndTurnsTheDayOverAsAsked(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	for _, row := range []struct {
		args []string
		want []string
	}{
		{[]string{"handoff"}, []string{"-m -"}},
		{[]string{"handoff", "-m", "   "}, []string{"-m -"}},
		{[]string{"handoff", "--retry", "-m", "another one"}, []string{"--retry"}},
		{[]string{"handoff", "-m", "x", "trellis"}, []string{`"trellis"`}},
		{[]string{"handoff", "-m", "x", "--sleep", "--nap"}, []string{"--sleep", "--nap"}},
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
	slept := s.Run(testworld.Invocation{Args: []string{"handoff", "-m", "-", "--sleep"}, Session: keel, Stdin: letter})
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
	if err := os.Remove(workdir); err != nil {
		t.Fatal(err)
	}
	stuck := s.Run(testworld.Invocation{Args: []string{"handoff", "-m", "Dear next trellis,", "--nap", "--session", trellis}, Session: keel})
	if stuck.Code != 1 {
		t.Fatalf("a nap that could not wake a successor exited %d: %s", stuck.Code, stuck.Stderr)
	}
	requireLines(t, "stdout", stuck.Stdout, "Trellis's letter is filed at ")
	requireLines(t, "stderr", stuck.Stderr, "handoff: no successor was woken: ", "`attn handoff --retry`")
	path := filedLetter(t, stuck.Stdout, "Trellis")
	if day := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession); day != trellis {
		t.Fatalf("after the failed nap trellis is bound to %q, want its day %s", day, trellis)
	}

	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	retried := s.Run(testworld.Invocation{Args: []string{"handoff", "--retry"}, Session: trellis})
	requireStdout(t, retried, "Trellis's letter was already filed at "+path+".\n", "Trellis's next day is session ")
	next := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession)
	if next == "" || next == trellis || !strings.Contains(retried.Stdout, "session "+next[:8]+", waking now") {
		t.Fatalf("the retry woke %q:\n%s", next, retried.Stdout)
	}
	s.Launched(next).Prompted()
	if filed, err := os.ReadFile(path); err != nil || string(filed) != "Dear next trellis,\n" {
		t.Fatalf("the retried letter reads %q (%v), want the one letter filed before", filed, err)
	}
}
