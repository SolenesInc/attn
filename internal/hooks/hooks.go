package hooks

import (
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/prompts"
	"strings"
)

type HookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type SettingsConfig struct {
	Hooks map[string][]HookEntry `json:"hooks"`
	// Claude Code copies every settings file's `env` block over the parent's environment, so a
	// knob only in the spawn environment loses; this file is passed with --settings and wins.
	Env map[string]string `json:"env,omitempty"`
}

type sessionStartHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

type sessionStartHookOutput struct {
	HookSpecificOutput sessionStartHookSpecificOutput `json:"hookSpecificOutput"`
}

var (
	AgentGuidance                 = prompts.RenderText("session", "agent-guidance", nil)
	GardenGuidance                = prompts.RenderText("session", "garden-guidance", nil)
	PullRequestSelfReportGuidance = prompts.RenderText("session", "pull-request-guidance", nil)
)

func WorkflowTriggerGuidance() string { return prompts.RenderText("session", "workflow-guidance", nil) }
func AgentInstructions(injectWorkflow bool) string {
	return (Launch{InjectWorkflow: injectWorkflow}).Instructions()
}

type Launch = prompts.Launch

func SessionStartOutput(contexts ...string) string {
	blocks := make([]string, 0, len(contexts))
	for _, context := range contexts {
		if context = strings.TrimSpace(context); context != "" {
			blocks = append(blocks, context)
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	output := sessionStartHookOutput{
		HookSpecificOutput: sessionStartHookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: strings.Join(blocks, "\n\n"),
		},
	}
	data, _ := json.Marshal(output)
	return string(data)
}

func ChiefGuidance(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return (Launch{NotebookRoot: root}).Instructions()
}

func Generate(sessionID, socketPath, wrapperPath string, env map[string]string) string {
	wrapper := strings.TrimSpace(wrapperPath)
	if wrapper == "" {
		wrapper = "attn"
	}
	wrapperCmd := shellQuote(wrapper)
	socketCmd := shellQuote(strings.TrimSpace(socketPath))

	config := SettingsConfig{
		Env: env,
		Hooks: map[string][]HookEntry{
			"SessionStart": {
				{
					Matcher: "startup|resume|clear|compact",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-session-start "%s"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			"Stop": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-stop "%s"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			"UserPromptSubmit": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-state "%s" "working" "user_prompt_submit"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			"PreToolUse": {
				{
					Matcher: "AskUserQuestion",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-state "%s" "waiting_input"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			// "permission_prompt" fires ~6s after a permission request and "idle_prompt" exactly 60s
			// after an unanswered turn settles — too slow to lead a transition, so it is evidence only.
			"Notification": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-notification "%s"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			"StopFailure": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-stop-failure "%s"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			// The two compaction hooks carry identical payloads, so the edge is named on the command line.
			"PreCompact": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-compact "%s" start`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			"PostCompact": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-compact "%s" end`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			"PermissionRequest": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-state "%s" "pending_approval"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
			"PostToolUse": {
				{
					Matcher: "TodoWrite",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-todo "%s"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
				{
					Matcher: "AskUserQuestion",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-state "%s" "working"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`ATTN_SOCKET_PATH=%s %s _hook-tool-use "%s"`, socketCmd, wrapperCmd, sessionID),
						},
					},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	return string(data)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func GenerateUnregisterCommand(sessionID, socketPath string) string {
	return fmt.Sprintf(`echo '{"cmd":"unregister","id":"%s"}' | nc -U %s`, sessionID, socketPath)
}
