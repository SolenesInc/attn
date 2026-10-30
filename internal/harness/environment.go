package harness

import (
	"os"
	"strings"
)

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

const nisseConversationPrefix = "nisse:"

func NisseConversationTerminal(conversation string) TerminalID {
	if conversation = strings.TrimSpace(conversation); conversation == "" {
		return ""
	}
	return TerminalID(nisseConversationPrefix + conversation)
}

func ParseNisseConversationTerminal(t TerminalID) (conversation string, ok bool) {
	conversation, ok = strings.CutPrefix(strings.TrimSpace(string(t)), nisseConversationPrefix)
	return conversation, ok && conversation != ""
}

// TerminalFromEnv reads the terminal identity, including the spelling used by older launches.
func TerminalFromEnv() TerminalID {
	if id := os.Getenv("ATTN_TERMINAL_ID"); id != "" {
		return TerminalID(id)
	}
	if profile := os.Getenv(CodexSharedProfileEnv); profile != "" {
		return CodexThreadTerminal(profile, os.Getenv("CODEX_THREAD_ID"))
	}
	if conversation := os.Getenv("NISSE_ROOT_CONVERSATION"); conversation != "" {
		return NisseConversationTerminal(conversation)
	}
	return TerminalID(os.Getenv("ATTN_SESSION_ID"))
}
