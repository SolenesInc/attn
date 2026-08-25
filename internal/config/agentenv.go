package config

import "os"

// Never inherit these into a freshly launched agent: a nested agent that reuses
// CLAUDE_CODE_SESSION_ID writes to its parent's transcript.
var agentSessionIdentityEnvKeys = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SSE_PORT",
}

// Tuning values a user may deliberately export from a shell profile, so scrub them only
// from long-lived attn processes that re-capture a clean login shell afterward.
var agentSessionTuningEnvKeys = []string{
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS",
	"CLAUDE_CODE_NO_FLICKER",
	"CLAUDE_EFFORT",
}

func ScrubAgentSessionIdentityEnv() []string {
	return scrubEnvKeys(agentSessionIdentityEnvKeys)
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
