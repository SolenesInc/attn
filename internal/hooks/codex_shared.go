package hooks

import (
	"fmt"
	"strings"
)

// MergeCodexOwnerConfig composes only Attn's invocation layer; native Codex
// composes user/project/plugin hooks itself from the ordinary home.
func MergeCodexOwnerConfig(config map[string]any, sessionID, socket, wrapper string, launch Launch) {
	if wrapper == "" {
		wrapper = "attn"
	}
	env := map[string]any{"ATTN_SESSION_ID": sessionID, "ATTN_SOCKET_PATH": socket, "ATTN_WRAPPER_PATH": wrapper}
	set := codexObject(codexObject(config, "shell_environment_policy"), "set")
	for k, v := range env {
		set[k] = v
	}
	features := codexObject(config, "features")
	features["hooks"] = true
	features["terminal_resize_reflow"] = true
	hooks := codexObject(config, "hooks")
	state := codexObject(hooks, "state")
	entries := []struct {
		name, key, matcher string
		args               []string
	}{
		{"SessionStart", "session_start", "startup|resume|clear|compact", []string{"_hook-session-start"}},
		{"UserPromptSubmit", "user_prompt_submit", "", []string{"_hook-state", "working", "user_prompt_submit"}},
		{"PermissionRequest", "permission_request", "*", []string{"_hook-state", "pending_approval"}},
		{"PreToolUse", "pre_tool_use", "*", []string{"_hook-state", "working"}},
		{"PostToolUse", "post_tool_use", "*", []string{"_hook-tool-use"}},
		{"Stop", "stop", "", []string{"_hook-stop"}},
	}
	for _, e := range entries {
		parts := []string{"env", shellQuote("ATTN_SESSION_ID=" + sessionID), shellQuote("ATTN_SOCKET_PATH=" + socket), shellQuote("ATTN_WRAPPER_PATH=" + wrapper), shellQuote(wrapper)}
		for _, arg := range e.args {
			parts = append(parts, shellQuote(arg))
		}
		command := strings.Join(parts, " ")
		groups, _ := hooks[e.name].([]any)
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}}
		if e.matcher != "" {
			group["matcher"] = e.matcher
		}
		key := fmt.Sprintf("/<session-flags>/config.toml:%s:%d:0", e.key, len(groups))
		state[key] = map[string]any{"trusted_hash": commandHookHash(e.key, e.matcher, command)}
		hooks[e.name] = append(groups, group)
	}
	if instructions := launch.Instructions(); instructions != "" {
		prior, _ := config["developer_instructions"].(string)
		config["developer_instructions"] = strings.TrimSpace(prior + "\n\n" + instructions)
	}
}

func codexObject(parent map[string]any, key string) map[string]any {
	if value, ok := parent[key].(map[string]any); ok {
		return value
	}
	value := make(map[string]any)
	parent[key] = value
	return value
}
