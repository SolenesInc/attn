package harness

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

const CodexSharedProfileEnv = "ATTN_CODEX_SHARED_PROFILE"

const CodexThreadPrefix = "codex:"

func CodexThreadTerminal(profile, thread string) TerminalID {
	if thread = strings.TrimSpace(thread); thread == "" {
		return ""
	}
	return TerminalID(CodexThreadPrefix + profile + ":" + thread)
}

func ParseCodexThreadTerminal(t TerminalID) (profile, thread string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(t)), CodexThreadPrefix)
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 || rest[i+1:] == "" {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

const (
	NisseConversationPrefix = "nisse:"
	SigmaConversationPrefix = "sigma:"
)

func NisseConversationTerminal(conversation string) TerminalID {
	return conversationTerminal(NisseConversationPrefix, conversation)
}

func ParseNisseConversationTerminal(t TerminalID) (conversation string, ok bool) {
	return parseConversationTerminal(NisseConversationPrefix, t)
}

func SigmaConversationTerminal(conversation string) TerminalID {
	return conversationTerminal(SigmaConversationPrefix, conversation)
}

func ParseSigmaConversationTerminal(t TerminalID) (conversation string, ok bool) {
	return parseConversationTerminal(SigmaConversationPrefix, t)
}

func conversationTerminal(prefix, conversation string) TerminalID {
	if conversation = strings.TrimSpace(conversation); conversation == "" {
		return ""
	}
	return TerminalID(prefix + conversation)
}

func parseConversationTerminal(prefix string, t TerminalID) (conversation string, ok bool) {
	conversation, ok = strings.CutPrefix(strings.TrimSpace(string(t)), prefix)
	return conversation, ok && conversation != ""
}

// TerminalFromEnv reads the terminal identity, including the spelling used by older launches.
func TerminalFromEnv() TerminalID {
	id, _ := ResolveTerminalFromEnv()
	return id
}

func ResolveTerminalFromEnv() (TerminalID, error) {
	if id := os.Getenv("ATTN_TERMINAL_ID"); id != "" {
		return TerminalID(id), nil
	}
	if profile := os.Getenv(CodexSharedProfileEnv); profile != "" {
		return CodexThreadTerminal(profile, os.Getenv("CODEX_THREAD_ID")), nil
	}
	if id := os.Getenv("ATTN_SESSION_ID"); id != "" {
		return TerminalID(id), nil
	}
	var names []string
	var caller TerminalID
	for _, entry := range os.Environ() {
		name, conversation, ok := strings.Cut(entry, "=")
		if ok && conversation != "" && len(name) > len("_ROOT_CONVERSATION") && strings.HasSuffix(name, "_ROOT_CONVERSATION") {
			names = append(names, name)
			caller = TerminalID("server-env:" + name + ":" + conversation)
		}
	}
	if len(names) > 1 {
		slices.Sort(names)
		return "", fmt.Errorf("conflicting caller variables: %s; keep only the current server's root conversation", strings.Join(names, ", "))
	}
	return caller, nil
}
