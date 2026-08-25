package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func GenerateCodexConfigOverrides(sessionID, socketPath, wrapperPath string, launch Launch) []string {
	wrapper := strings.TrimSpace(wrapperPath)
	if wrapper == "" {
		wrapper = "attn"
	}

	command := func(args ...string) string {
		parts := []string{shellQuote(wrapper)}
		for _, arg := range args {
			parts = append(parts, shellQuote(arg))
		}
		return strings.Join(parts, " ")
	}

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

	overrides := []string{
		// Codex applies its shell environment policy per tool working directory, so values
		// inherited by the top-level process vanish under a child worktree. Pin them here.
		"shell_environment_policy.set.ATTN_SESSION_ID=" + strconv.Quote(strings.TrimSpace(sessionID)),
		"shell_environment_policy.set.ATTN_WRAPPER_PATH=" + strconv.Quote(wrapper),
		"features.hooks=true",
		// Reflow enabled: without it a resized inline UI leaves stale headers.
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
