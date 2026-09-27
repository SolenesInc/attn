package main_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/testworld"
)

func TestInstanceEnvLeavesTheShellOnTheChosenInstanceWithNoRoutingOverrideBehind(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	attn := testworld.AttnBinary(t)
	inherited := []string{"ATTN_INSTANCE=elsewhere"}
	for _, name := range config.RoutingOverrideEnv() {
		inherited = append(inherited, name+"=/stale/"+name)
	}

	for _, tc := range []struct {
		args     []string
		instance string
	}{
		{args: []string{"stackprobe"}, instance: "stackprobe"},
		{args: []string{"--unset"}},
		{args: []string{"default"}},
	} {
		script := `eval "$("$ATTN" instance-env ` + strings.Join(tc.args, " ") + `)" && env`
		got := s.Run(testworld.Invocation{Binary: "/bin/sh", Args: []string{"-c", script}, Env: append([]string{"ATTN=" + attn}, inherited...)})
		env := strings.Split(got.Stdout, "\n")
		var left []string
		for _, name := range config.RoutingOverrideEnv() {
			if slices.ContainsFunc(env, func(pair string) bool { return strings.HasPrefix(pair, name+"=") }) {
				left = append(left, name)
			}
		}
		selected := slices.ContainsFunc(env, func(pair string) bool { return strings.HasPrefix(pair, "ATTN_INSTANCE=") })
		if got.Code != 0 || len(left) > 0 || selected != (tc.instance != "") || (selected && !slices.Contains(env, "ATTN_INSTANCE="+tc.instance)) {
			t.Errorf("eval of attn instance-env %q exited %d leaving overrides %q and ATTN_INSTANCE set: %t; want only ATTN_INSTANCE=%q\nstderr: %s", tc.args, got.Code, left, selected, tc.instance, got.Stderr)
		}
	}

	for _, tc := range []struct {
		args []string
		last string
	}{
		{args: []string{"--fish", "stackprobe"}, last: "set -gx ATTN_INSTANCE stackprobe"},
		{args: []string{"--fish", "--unset"}, last: "set -e ATTN_INSTANCE"},
	} {
		got := s.Attn(append([]string{"instance-env"}, tc.args...)...)
		lines := strings.Split(strings.TrimSpace(got.Stdout), "\n")
		for _, name := range config.RoutingOverrideEnv() {
			if !slices.Contains(lines, "set -e "+name) {
				t.Errorf("attn instance-env %q does not erase %s:\n%s", tc.args, name, got.Stdout)
			}
		}
		if got.Code != 0 || lines[len(lines)-1] != tc.last {
			t.Errorf("attn instance-env %q exited %d ending with %q, want %q", tc.args, got.Code, lines[len(lines)-1], tc.last)
		}
	}

	if refused := s.Attn("instance-env", "Not A Name"); refused.Code != 1 || refused.Stdout != "" {
		t.Errorf("attn instance-env with an invalid name exited %d printing %q, want a refusal with nothing to eval", refused.Code, refused.Stdout)
	}
}
