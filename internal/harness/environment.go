package harness

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

const CodexSharedProfileEnv = "ATTN_CODEX_SHARED_PROFILE"

const CodexThreadEnv = "CODEX_THREAD_ID"

const ServerCallerPrefix = "server-env:"

type ServerCaller struct{ Variable, Scope, Conversation string }

func (c ServerCaller) Terminal() TerminalID {
	variable := c.Variable
	if c.Scope != "" {
		variable += "@" + c.Scope
	}
	return TerminalID(ServerCallerPrefix + variable + ":" + c.Conversation)
}

func ParseServerCaller(t TerminalID) (ServerCaller, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(t)), ServerCallerPrefix)
	if !ok {
		return ServerCaller{}, false
	}
	named, conversation, ok := strings.Cut(rest, ":")
	if !ok {
		return ServerCaller{}, false
	}
	variable, scope, _ := strings.Cut(named, "@")
	return ServerCaller{Variable: variable, Scope: scope, Conversation: conversation}, true
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
		thread := strings.TrimSpace(os.Getenv(CodexThreadEnv))
		if thread == "" {
			return "", nil
		}
		return ServerCaller{Variable: CodexThreadEnv, Scope: profile, Conversation: thread}.Terminal(), nil
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
			caller = ServerCaller{Variable: name, Conversation: conversation}.Terminal()
		}
	}
	if len(names) > 1 {
		slices.Sort(names)
		return "", fmt.Errorf("conflicting caller variables: %s; keep only the current server's root conversation", strings.Join(names, ", "))
	}
	return caller, nil
}
