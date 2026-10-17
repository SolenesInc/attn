package config

import (
	"os"
	"slices"
	"strings"
)

var agentSessionIdentityEnvKeys = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SSE_PORT",
	"ATTN_AGENT_GUIDANCE",
	"ATTN_CHIEF_GUIDANCE",
}

var agentSessionTuningEnvKeys = []string{
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS",
	"CLAUDE_CODE_NO_FLICKER",
	"CLAUDE_EFFORT",
}

func WithoutAgentSessionIdentityEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(agentSessionIdentityEnvKeys, key) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func ScrubInheritedAgentSessionEnv() []string {
	scrubbed := scrubEnvKeys(agentSessionIdentityEnvKeys)
	return append(scrubbed, scrubEnvKeys(agentSessionTuningEnvKeys)...)
}

func scrubEnvKeys(keys []string) []string {
	scrubbed := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := os.LookupEnv(key); ok {
			_ = os.Unsetenv(key)
			scrubbed = append(scrubbed, key)
		}
	}
	return scrubbed
}
