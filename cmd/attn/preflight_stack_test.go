package main_test

import (
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
