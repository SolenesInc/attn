package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestInsideASessionAttnReportsPresenceAndRefusesWhatItCannotLaunch(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	for _, tc := range []struct {
		name string
		args []string
		env  []string
		code int
		want string
	}{
		{name: "outside attn, whatever session id lingers", args: []string{"presence"}, env: []string{"ATTN_SESSION_ID=stale-session"}, code: 1, want: "not running inside attn"},
		{name: "inside attn without a session", args: []string{"presence"}, env: []string{"ATTN_INSIDE_APP=1"}, want: "running inside attn"},
		{name: "no session id outside attn", args: []string{"presence", "--session-id"}, env: []string{"ATTN_SESSION_ID=stale-session"}, code: 1},
		{name: "no session id inside attn without a session", args: []string{"presence", "--session-id"}, env: []string{"ATTN_INSIDE_APP=1"}, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Run(testworld.Invocation{Args: tc.args, Env: tc.env})
			if got.Code != tc.code || strings.TrimSpace(got.Stdout) != tc.want {
				t.Errorf("attn %q exited %d printing %q, want %d and %q", tc.args, got.Code, got.Stdout, tc.code, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"random"}, want: `unknown command "random"`},
		{args: []string{"workflow", "run", "obsolete.js"}, want: `unknown command "workflow"`},
		{args: []string{"_workflow-result-mcp"}, want: `unknown command "_workflow-result-mcp"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got := s.Run(testworld.Invocation{Args: tc.args, Session: "session-1"})
			if got.Code != 1 || !strings.Contains(got.Stderr, tc.want) || !strings.Contains(got.Stderr, "usage: attn <command>") {
				t.Errorf("attn %q exited %d with stderr:\n%s\nwant 1, %q and the usage", tc.args, got.Code, got.Stderr, tc.want)
			}
		})
	}
}
