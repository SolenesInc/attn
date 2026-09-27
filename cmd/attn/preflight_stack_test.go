package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

type resolvedLaunch struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

func TestPreflightReportsEachLaunchSettingWithWhereItCameFrom(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	for _, tc := range []struct {
		name                 string
		args                 []string
		env                  []string
		agent, model, effort resolvedLaunch
	}{
		{
			name:   "defaults",
			agent:  resolvedLaunch{Value: "codex", Source: "default"},
			model:  resolvedLaunch{Source: "agent_default"},
			effort: resolvedLaunch{Source: "agent_default"},
		},
		{
			name:   "a flag over the environment",
			args:   []string{"--model", "flag-model"},
			env:    []string{"ATTN_AGENT=claude", "ATTN_MODEL=env-model", "ATTN_EFFORT=low"},
			agent:  resolvedLaunch{Value: "claude", Source: "environment"},
			model:  resolvedLaunch{Value: "flag-model", Source: "explicit"},
			effort: resolvedLaunch{Value: "low", Source: "environment"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var report struct {
				Launch struct {
					Agent  resolvedLaunch `json:"agent"`
					Model  resolvedLaunch `json:"model"`
					Effort resolvedLaunch `json:"effort"`
				} `json:"launch"`
			}
			got := s.Run(testworld.Invocation{Args: append([]string{"preflight", "--json"}, tc.args...), Env: tc.env})
			got.JSON(t, &report)
			if report.Launch.Agent != tc.agent || report.Launch.Model != tc.model || report.Launch.Effort != tc.effort {
				t.Errorf("preflight launch = %+v, want agent %+v, model %+v, effort %+v", report.Launch, tc.agent, tc.model, tc.effort)
			}
		})
	}
}

func TestPreflightNamesTheRootCauseAndTheActionOfEachFailingCheck(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	elsewhere := filepath.Join(s.Dir, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, check, status, summary, action string
		inv                                  testworld.Invocation
	}{
		{
			name: "a required tool missing from PATH", check: "tool.git", status: "fail",
			summary: `required tool "git" was not found on PATH`, action: "Install git",
			inv: testworld.Invocation{Env: []string{"PATH=" + elsewhere}},
		},
		{
			name: "an unwritable Go build cache", check: "path.go_build_cache", status: "fail",
			summary: "/etc/passwd/cache is not writable", action: "Point Go at a writable cache",
			inv: testworld.Invocation{Env: []string{"GOCACHE=/etc/passwd/cache"}},
		},
		{
			name: "an agent the daemon does not provide", check: "launch.agent", status: "fail",
			summary: `daemon does not provide agent "nosuch"`, action: "Select one of the daemon's agents",
			inv: testworld.Invocation{Args: []string{"--agent", "nosuch"}},
		},
		{
			name: "a relative data dir read from the daemon's directory", check: "routing.daemon", status: "pass",
			summary: "instance=default",
			inv:     testworld.Invocation{Env: []string{"ATTN_DATA_DIR=.", "ATTN_SOCKET_PATH=" + s.Socket}, Dir: s.Dir},
		},
		{
			name: "the same relative data dir read from another directory", check: "routing.daemon", status: "fail",
			summary: "daemon routing mismatch", action: "clear inherited routing overrides",
			inv: testworld.Invocation{Env: []string{"ATTN_DATA_DIR=.", "ATTN_SOCKET_PATH=" + s.Socket}, Dir: elsewhere},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var report struct {
				Checks []struct {
					Name    string `json:"name"`
					Status  string `json:"status"`
					Summary string `json:"summary"`
					Action  string `json:"action"`
				} `json:"checks"`
			}
			inv := tc.inv
			inv.Args = append([]string{"preflight", "--json"}, inv.Args...)
			s.Run(inv).JSON(t, &report)
			for _, c := range report.Checks {
				if c.Name != tc.check {
					continue
				}
				if c.Status != tc.status || !strings.Contains(c.Summary, tc.summary) || !strings.Contains(c.Action, tc.action) {
					t.Fatalf("%s = %+v, want %s with %q and action %q", tc.check, c, tc.status, tc.summary, tc.action)
				}
				return
			}
			t.Fatalf("preflight reported no %s check: %+v", tc.check, report.Checks)
		})
	}
}
