package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSkillCommandPrintsTheBundledSkillAndItsReferences(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	requireStdout(t, s.Attn("skill"), "name: attn")
	listed := s.Attn("skill", "--list")
	requireStdout(t, listed, "garden\n", "delegation\n")
	if strings.Contains(listed.Stdout, "workflow\n") {
		t.Errorf("skill still advertises the removed engine reference: %s", listed.Stdout)
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
		{args: []string{"--reference", "nope"}, code: 1, want: []string{`"nope"`, "garden"}},
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
