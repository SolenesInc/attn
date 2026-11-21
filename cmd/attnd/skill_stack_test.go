package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSkillCommandPrintsTheMapAndItsReferences(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	t.Run("with no daemon running", func(t *testing.T) { requireSkill(t, s) })
	s.Start()
	unopenable := filepath.Join(s.Dir, "unopenable.sock")
	if err := os.WriteFile(unopenable, nil, 0); err != nil {
		t.Fatal(err)
	}
	blocked := s.Run(testworld.Invocation{Args: []string{"skill", "--reference", "garden"}, Env: []string{"ATTN_SOCKET_PATH=" + unopenable}})
	requireStdout(t, blocked, "# The garden")
	t.Run("through the daemon", func(t *testing.T) { requireSkill(t, s) })
}

func requireSkill(t *testing.T, s *testworld.Stack) {
	requireStdout(t, s.Attn("skill"), "## What attn can do")
	listed := s.Attn("skill", "--list")
	requireStdout(t, listed, "garden\n", "delegation\n")
	if strings.Contains(listed.Stdout, "workflow\n") {
		t.Errorf("skill still advertises the removed engine reference: %s", listed.Stdout)
	}
	if shell := s.Run(testworld.Invocation{Args: []string{"skill"}, Terminal: "a-shell-with-no-session"}); shell.Code != 0 || !strings.Contains(shell.Stdout, "## What attn can do") {
		t.Errorf("attn skill from a terminal that shows no session exited %d:\n%s%s", shell.Code, shell.Stdout, shell.Stderr)
	}
	garden := s.Attn("skill", "--reference", "garden")
	requireStdout(t, garden, "# The garden")
	if withSuffix := s.Attn("skill", "--reference", "garden.md"); withSuffix.Code != 0 || withSuffix.Stdout != garden.Stdout {
		t.Errorf("--reference garden.md exited %d and printed a different reference than --reference garden:\n%s", withSuffix.Code, withSuffix.Stdout)
	}

	for _, tc := range []struct {
		args []string
		code int
		want []string
	}{
		{args: []string{"--reference", "workflow"}, code: 1, want: []string{`"workflow"`, "garden"}},
		{args: []string{"--list", "--reference", "garden"}, code: 2, want: []string{"mutually exclusive"}},
		{args: []string{"--reference"}, code: 2, want: []string{"--list"}},
	} {
		refused := s.Attn(append([]string{"skill"}, tc.args...)...)
		if refused.Code != tc.code || refused.Stdout != "" {
			t.Errorf("attn skill %s exited %d with stdout %q, want %d and nothing on stdout", strings.Join(tc.args, " "), refused.Code, refused.Stdout, tc.code)
		}
		requireLines(t, "attn skill "+strings.Join(tc.args, " "), refused.Stderr, tc.want...)
	}
}
