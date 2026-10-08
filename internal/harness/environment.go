package harness

import (
	"os"
	"strings"
)

// CodexSharedProfileEnv marks a process a profile's shared Codex app-server runs: a hook or a tool
// of one of its conversations, which speaks as that conversation rather than as a terminal.
const CodexSharedProfileEnv = "ATTN_CODEX_SHARED_PROFILE"

const codexThreadPrefix = "codex:"

func CodexThreadTerminal(profile, thread string) TerminalID {
	if thread = strings.TrimSpace(thread); thread == "" {
		return ""
	}
	return TerminalID(codexThreadPrefix + profile + ":" + thread)
}

func ParseCodexThreadTerminal(t TerminalID) (profile, thread string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(t)), codexThreadPrefix)
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 || rest[i+1:] == "" {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// TerminalFromEnv reads the terminal identity, including the spelling used by older launches.
func TerminalFromEnv() TerminalID {
	if profile, shared := os.LookupEnv(CodexSharedProfileEnv); shared {
		return CodexThreadTerminal(profile, os.Getenv("CODEX_THREAD_ID"))
	}
	id := os.Getenv("ATTN_TERMINAL_ID")
	if id == "" {
		id = os.Getenv("ATTN_SESSION_ID")
	}
	return TerminalID(id)
}
