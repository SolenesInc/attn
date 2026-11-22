package harness

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

const ServerCallerPrefix = "server-env:"

type CallerEnv struct {
	Scope        string
	Conversation string
}

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

func CallerEnvKeys(callers ...CallerEnv) []string {
	var keys []string
	add := func(k string) {
		if k != "" && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, c := range callers {
		add(c.Scope)
		add(c.Conversation)
	}
	return keys
}

func TerminalFromEnv(callers ...CallerEnv) TerminalID {
	id, _ := ResolveTerminalFromEnv(callers...)
	return id
}

func ResolveTerminalFromEnv(callers ...CallerEnv) (TerminalID, error) {
	if id := os.Getenv("ATTN_TERMINAL_ID"); id != "" {
		return TerminalID(id), nil
	}
	for _, c := range callers {
		if c.Scope == "" || c.Conversation == "" {
			continue
		}
		if scope := os.Getenv(c.Scope); scope != "" {
			conversation := strings.TrimSpace(os.Getenv(c.Conversation))
			if conversation == "" {
				return "", nil
			}
			return ServerCaller{Variable: c.Conversation, Scope: scope, Conversation: conversation}.Terminal(), nil
		}
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
