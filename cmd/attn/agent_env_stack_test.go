package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func envValue(env []string, key string) (string, bool) {
	for _, pair := range env {
		if value, found := strings.CutPrefix(pair, key+"="); found {
			return value, true
		}
	}
	return "", false
}

func TestAgentsNeverInheritTheClaudeSessionThatStartedTheDaemon(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	session := []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_SSE_PORT",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS", "CLAUDE_CODE_NO_FLICKER", "CLAUDE_EFFORT"}
	for _, key := range session {
		s.Vars = append(s.Vars, key+"=from-the-parent-claude")
	}
	s.Vars = append(s.Vars, "CLAUDE_CODE_USE_BEDROCK=1", "ANTHROPIC_API_KEY=sk-test")
	s.Start()

	id := s.Spawn(s.App(), fakeagent.Claude, s.Path("shop"))
	env := s.Launched(id).Env
	for _, key := range session {
		if value, found := envValue(env, key); found && value == "from-the-parent-claude" {
			t.Errorf("claude inherited %s=%s from the Claude session that started the daemon", key, value)
		}
	}
	for key, want := range map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_API_KEY": "sk-test"} {
		if got, _ := envValue(env, key); got != want {
			t.Errorf("claude got %s=%q, want the user's %q", key, got, want)
		}
	}
}
