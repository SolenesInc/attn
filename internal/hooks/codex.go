package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const CodexThreadHooksEnv = "ATTN_CODEX_THREAD_HOOKS"

const CodexThreadCallerPrefix = "codex:"

func CodexThreadCaller(profile, conversation string) string {
	return CodexThreadCallerPrefix + profile + ":" + conversation
}

func ParseCodexThreadCaller(id string) (profile, conversation string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(id), CodexThreadCallerPrefix)
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 || rest[i+1:] == "" {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

func GenerateCodexConfigOverrides(terminalID, socketPath, wrapperPath string, launch Launch) []string {
	wrapper := hookWrapper(wrapperPath)
	overrides := []string{
		"shell_environment_policy.set.ATTN_TERMINAL_ID=" + strconv.Quote(strings.TrimSpace(terminalID)),
		"shell_environment_policy.set.ATTN_WRAPPER_PATH=" + strconv.Quote(wrapper),
	}
	overrides = append(overrides, codexHookOverrides(codexHookCommand(wrapper, ""))...)
	if socket := strings.TrimSpace(socketPath); socket != "" {
		overrides = append(overrides,
			"shell_environment_policy.set.ATTN_SOCKET_PATH="+strconv.Quote(socket),
		)
	}
	if instructions := launch.Instructions(); instructions != "" {
		overrides = append(overrides, "developer_instructions="+strconv.Quote(instructions))
	}
	return overrides
}

func GenerateCodexServerConfigOverrides(wrapperPath, profile string) []string {
	return codexHookOverrides(codexHookCommand(hookWrapper(wrapperPath), "env "+shellQuote(CodexThreadHooksEnv+"="+profile)+" "))
}

func hookWrapper(wrapperPath string) string {
	if wrapper := strings.TrimSpace(wrapperPath); wrapper != "" {
		return wrapper
	}
	return "attn"
}

func codexHookCommand(wrapper, prefix string) func(args ...string) string {
	return func(args ...string) string {
		parts := []string{shellQuote(wrapper)}
		for _, arg := range args {
			parts = append(parts, shellQuote(arg))
		}
		return prefix + strings.Join(parts, " ")
	}
}

func codexHookOverrides(command func(args ...string) string) []string {
	hook := func(command string) string {
		return fmt.Sprintf(`{ type = "command", command = %s, timeout = 5 }`, strconv.Quote(command))
	}
	group := func(matcher string, command string) string {
		if strings.TrimSpace(matcher) == "" {
			return fmt.Sprintf(`[{ hooks = [%s] }]`, hook(command))
		}
		return fmt.Sprintf(`[{ matcher = %s, hooks = [%s] }]`, strconv.Quote(matcher), hook(command))
	}

	sessionStart := command("_hook-session-start")
	userPromptSubmit := command("_hook-state", "working", "user_prompt_submit")
	permissionRequest := command("_hook-state", "pending_approval")
	preToolUse := command("_hook-state", "working")
	postToolUse := command("_hook-tool-use")
	stop := command("_hook-stop")

	return []string{
		"features.hooks=true",
		"features.terminal_resize_reflow=true",
		trustedHashOverrides([]codexHookTrustEntry{
			{eventKey: "session_start", matcher: "startup|resume|clear|compact", command: sessionStart},
			{eventKey: "user_prompt_submit", command: userPromptSubmit},
			{eventKey: "permission_request", matcher: "*", command: permissionRequest},
			{eventKey: "pre_tool_use", matcher: "*", command: preToolUse},
			{eventKey: "post_tool_use", matcher: "*", command: postToolUse},
			{eventKey: "stop", command: stop},
		}),
		"hooks.SessionStart=" + group("startup|resume|clear|compact", sessionStart),
		"hooks.UserPromptSubmit=" + group("", userPromptSubmit),
		"hooks.PermissionRequest=" + group("*", permissionRequest),
		"hooks.PreToolUse=" + group("*", preToolUse),
		"hooks.PostToolUse=" + group("*", postToolUse),
		"hooks.Stop=" + group("", stop),
	}
}

type codexHookTrustEntry struct {
	eventKey string
	matcher  string
	command  string
}

func trustedHashOverrides(entries []codexHookTrustEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		key := fmt.Sprintf("/<session-flags>/config.toml:%s:0:0", entry.eventKey)
		parts = append(parts, fmt.Sprintf(
			"%s = { trusted_hash = %s }",
			strconv.Quote(key),
			strconv.Quote(commandHookHash(entry.eventKey, entry.matcher, entry.command)),
		))
	}
	return fmt.Sprintf("hooks.state={ %s }", strings.Join(parts, ", "))
}

func commandHookHash(eventKey, matcher, command string) string {
	group := map[string]any{
		"event_name": eventKey,
		"hooks": []any{
			map[string]any{
				"async":   false,
				"command": command,
				"timeout": 5,
				"type":    "command",
			},
		},
	}
	if normalized := normalizedMatcher(eventKey, matcher); normalized != "" {
		group["matcher"] = normalized
	}

	serialized, err := json.Marshal(group)
	if err != nil {
		panic(fmt.Sprintf("failed to hash Codex hook identity: %v", err))
	}
	sum := sha256.Sum256(serialized)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedMatcher(eventKey, matcher string) string {
	switch eventKey {
	case "user_prompt_submit", "stop":
		return ""
	default:
		return strings.TrimSpace(matcher)
	}
}
