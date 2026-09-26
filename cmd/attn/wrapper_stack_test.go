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
		env  []string
		code int
		want string
	}{
		{name: "outside attn, whatever session id lingers", env: []string{"ATTN_SESSION_ID=stale-session"}, code: 1, want: "not running inside attn"},
		{name: "inside a session", env: []string{"ATTN_INSIDE_APP=1", "ATTN_SESSION_ID= session-1 "}, want: "running inside attn (session session-1)"},
		{name: "inside attn without a session", env: []string{"ATTN_INSIDE_APP=1"}, want: "running inside attn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Run(testworld.Invocation{Args: []string{"presence"}, Env: tc.env})
			if got.Code != tc.code || strings.TrimSpace(got.Stdout) != tc.want {
				t.Errorf("attn presence exited %d printing %q, want %d and %q", got.Code, got.Stdout, tc.code, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--model", "foo"}, want: `unknown flag "--model"`},
		{args: []string{"--"}, want: `unknown flag "--"`},
		{args: []string{"-s"}, want: "flag -s needs a value"},
		{args: []string{"--member"}, want: "flag --member needs a value"},
		{args: []string{"--yolo", "random"}, want: `unknown flag "random"`},
		{args: []string{"random"}, want: `unknown command "random"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got := s.Run(testworld.Invocation{Args: tc.args, Session: "session-1"})
			if got.Code != 1 || !strings.Contains(got.Stderr, tc.want) || !strings.Contains(got.Stderr, "usage: attn <command>") {
				t.Errorf("attn %q exited %d with stderr:\n%s\nwant 1, %q and the usage", tc.args, got.Code, got.Stderr, tc.want)
			}
		})
	}
}
