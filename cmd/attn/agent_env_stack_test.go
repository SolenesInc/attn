package main_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

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

func TestAttnRunInsideAClaudeSessionKeepsTheUsersTuningButNotItsIdentity(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	work := s.Path("shop")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}

	id := uuid.NewString()
	launch := s.LaunchInTerminal(testworld.Invocation{Dir: work, Env: []string{
		"ATTN_INSIDE_APP=1", "ATTN_AGENT=claude", "ATTN_SESSION_ID=" + id,
		"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=leaked", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_EFFORT=xhigh", "CLAUDE_CODE_NO_FLICKER=1",
	}})
	claude := s.Launched(id)
	for _, key := range []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT"} {
		if value, found := envValue(claude.Env, key); found && slices.Contains([]string{"1", "leaked", "cli"}, value) {
			t.Errorf("claude inherited the outer session's %s=%s", key, value)
		}
	}
	for key, want := range map[string]string{"CLAUDE_EFFORT": "xhigh", "CLAUDE_CODE_NO_FLICKER": "1"} {
		if got, _ := envValue(claude.Env, key); got != want {
			t.Errorf("claude got %s=%q, want the user's %q kept", key, got, want)
		}
	}
	claude.Exit(0)
	launch.Wait()
}
