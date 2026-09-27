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
	requireStdout(t, listed, "tickets\n", "delegation\n", "workflow\n")
	tickets := s.Attn("skill", "--reference", "tickets")
	requireStdout(t, tickets, "# Tickets retired")
	if withSuffix := s.Attn("skill", "--reference", "tickets.md"); withSuffix.Code != 0 || withSuffix.Stdout != tickets.Stdout {
		t.Errorf("--reference tickets.md exited %d and printed a different reference than --reference tickets:\n%s", withSuffix.Code, withSuffix.Stdout)
	}

	for _, tc := range []struct {
		args []string
		code int
		want []string
	}{
		{args: []string{"--reference", "nope"}, code: 1, want: []string{`"nope"`, "tickets"}},
		{args: []string{"--list", "--reference", "tickets"}, code: 2, want: []string{"mutually exclusive"}},
		{args: []string{"--reference"}, code: 2, want: []string{"--list"}},
	} {
		refused := s.Attn(append([]string{"skill"}, tc.args...)...)
		if refused.Code != tc.code || refused.Stdout != "" {
			t.Errorf("attn skill %s exited %d with stdout %q, want %d and nothing on stdout", strings.Join(tc.args, " "), refused.Code, refused.Stdout, tc.code)
		}
		requireLines(t, "attn skill "+strings.Join(tc.args, " "), refused.Stderr, tc.want...)
	}
}
