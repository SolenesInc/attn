package hooks

// A tool, not a test: asserts nothing and skips unless ATTN_HOOKLOG_SCRIPT points at
// a logger script. Run with -run TestPrintCodexHookOverridesForManualCapture -v.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestPrintCodexHookOverridesForManualCapture(t *testing.T) {
	logger := strings.TrimSpace(os.Getenv("ATTN_HOOKLOG_SCRIPT"))
	if logger == "" {
		t.Skip("set ATTN_HOOKLOG_SCRIPT to the hook logger script")
	}

	events := []struct {
		key     string
		matcher string
		name    string
	}{
		{"session_start", "startup|resume|clear|compact", "SessionStart"},
		{"user_prompt_submit", "", "UserPromptSubmit"},
		{"permission_request", "*", "PermissionRequest"},
		{"pre_tool_use", "*", "PreToolUse"},
		{"post_tool_use", "*", "PostToolUse"},
		{"stop", "", "Stop"},
	}

	hook := func(command string) string {
		return fmt.Sprintf(`{ type = "command", command = %s, timeout = 5 }`, strconv.Quote(command))
	}
	group := func(matcher, command string) string {
		if strings.TrimSpace(matcher) == "" {
			return fmt.Sprintf(`[{ hooks = [%s] }]`, hook(command))
		}
		return fmt.Sprintf(`[{ matcher = %s, hooks = [%s] }]`, strconv.Quote(matcher), hook(command))
	}

	var trust []string
	var out []string
	out = append(out, "features.hooks=true")
	for _, e := range events {
		command := shellQuote(logger) + " " + shellQuote(e.name)
		key := fmt.Sprintf("/<session-flags>/config.toml:%s:0:0", e.key)
		trust = append(trust, fmt.Sprintf("%s = { trusted_hash = %s }",
			strconv.Quote(key),
			strconv.Quote(commandHookHash(e.key, e.matcher, command))))

		tomlKey := map[string]string{
			"session_start":      "SessionStart",
			"user_prompt_submit": "UserPromptSubmit",
			"permission_request": "PermissionRequest",
			"pre_tool_use":       "PreToolUse",
			"post_tool_use":      "PostToolUse",
			"stop":               "Stop",
		}[e.key]
		out = append(out, "hooks."+tomlKey+"="+group(e.matcher, command))
	}
	out = append(out, fmt.Sprintf("hooks.state={ %s }", strings.Join(trust, ", ")))

	for _, o := range out {
		fmt.Println("OVERRIDE\t" + o)
	}
}
