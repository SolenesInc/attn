package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateHooks(t *testing.T) {
	sessionID := "abc123"
	socketPath := "/home/user/.claude-manager.sock"

	settings := Generate(sessionID, socketPath, "/tmp/attn", nil)

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Check hooks exist as a map
	hooksMap, ok := parsed["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks field not found or not a map")
	}

	// Should have 3 event types
	if len(hooksMap) < 3 {
		t.Errorf("expected at least 3 event types, got %d", len(hooksMap))
	}

	// Verify each event type has hook entries
	for eventType, entries := range hooksMap {
		entriesArray, ok := entries.([]interface{})
		if !ok {
			t.Errorf("event %s: expected array of entries", eventType)
			continue
		}
		for _, entry := range entriesArray {
			hook := entry.(map[string]interface{})
			if _, ok := hook["matcher"]; !ok {
				t.Errorf("event %s: hook missing matcher", eventType)
			}
			if _, ok := hook["hooks"]; !ok {
				t.Errorf("event %s: hook missing hooks array", eventType)
			}
		}
	}
}

// The catch-all PostToolUse hook is the one that fires on every tool call, so
// it stays a single spawn: _hook-tool-use resets the working state AND records
// any markdown the call wrote, instead of a second hook entry doing the latter.
func TestGenerateHooks_CatchAllPostToolUseIsOneSpawn(t *testing.T) {
	settings := Generate("abc123", "/tmp/test.sock", "/tmp/attn", nil)

	var parsed SettingsConfig
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var catchAll []Hook
	for _, entry := range parsed.Hooks["PostToolUse"] {
		if entry.Matcher == "*" {
			catchAll = entry.Hooks
		}
	}
	if len(catchAll) != 1 {
		t.Fatalf("catch-all PostToolUse has %d commands, want exactly 1", len(catchAll))
	}
	if !strings.Contains(catchAll[0].Command, `_hook-tool-use "abc123"`) {
		t.Errorf("catch-all PostToolUse command = %q, want _hook-tool-use", catchAll[0].Command)
	}
}

func TestGenerateCodexConfigOverrides_PostToolUseRecordsEdits(t *testing.T) {
	overrides := strings.Join(GenerateCodexConfigOverrides("abc123", "/tmp/test.sock", "/tmp/attn", Launch{}), "\n")

	if !strings.Contains(overrides, "hooks.PostToolUse=[{ matcher = \"*\", hooks = [{ type = \"command\", command = \"'/tmp/attn' '_hook-tool-use'\"") {
		t.Fatalf("codex PostToolUse should run _hook-tool-use: %q", overrides)
	}
	// Codex only runs a hook whose recorded hash matches, so the trust entry
	// has to move with the command.
	if !strings.Contains(overrides, "post_tool_use") {
		t.Fatalf("codex overrides should trust the post_tool_use hook: %q", overrides)
	}
}

func TestGenerateHooks_ContainsSessionID(t *testing.T) {
	sessionID := "unique-session-id-12345"
	socketPath := "/tmp/test.sock"

	hooks := Generate(sessionID, socketPath, "/tmp/attn", nil)

	if !strings.Contains(hooks, sessionID) {
		t.Error("generated hooks should contain session ID")
	}
}

func TestGenerateHooks_ContainsSocketPath(t *testing.T) {
	sessionID := "test"
	socketPath := "/custom/path/to/socket.sock"

	hooks := Generate(sessionID, socketPath, "/tmp/attn", nil)

	if !strings.Contains(hooks, socketPath) {
		t.Error("generated hooks should contain socket path")
	}
}

func TestGenerateHooks_HasStopHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/tmp/attn", nil)

	if !strings.Contains(hooks, "Stop") {
		t.Error("hooks should include Stop event for waiting state")
	}
}

func TestGenerateHooks_HasSessionStartHook(t *testing.T) {
	var parsed SettingsConfig
	if err := json.Unmarshal([]byte(Generate("test", "/tmp/test.sock", "/tmp/attn", nil)), &parsed); err != nil {
		t.Fatalf("invalid Claude hook JSON: %v", err)
	}
	entries := parsed.Hooks["SessionStart"]
	if len(entries) != 1 || entries[0].Matcher != "startup|resume|clear|compact" || len(entries[0].Hooks) != 1 {
		t.Fatalf("Claude SessionStart hook = %+v", entries)
	}
	if command := entries[0].Hooks[0].Command; !strings.Contains(command, `_hook-session-start "test"`) {
		t.Fatalf("Claude SessionStart command = %q", command)
	}
}

// The Notification hook is the only harness-owned signal that says the agent is
// blocked on the user *and* says why. Losing the registration would leave that
// evidence silently absent rather than failing anything.
func TestGenerateHooks_HasNotificationHook(t *testing.T) {
	var parsed SettingsConfig
	if err := json.Unmarshal([]byte(Generate("abc123", "/tmp/test.sock", "/tmp/attn", nil)), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entries := parsed.Hooks["Notification"]
	if len(entries) != 1 || len(entries[0].Hooks) != 1 {
		t.Fatalf("want one Notification hook, got %+v", entries)
	}
	if cmd := entries[0].Hooks[0].Command; !strings.Contains(cmd, `_hook-notification "abc123"`) {
		t.Fatalf("Notification command = %q, want _hook-notification for the session", cmd)
	}
}

func TestGenerateHooks_HasUserPromptSubmitHook(t *testing.T) {
	var parsed SettingsConfig
	if err := json.Unmarshal([]byte(Generate("test", "/tmp/test.sock", "/tmp/attn", nil)), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	entries := parsed.Hooks["UserPromptSubmit"]
	if len(entries) != 1 || len(entries[0].Hooks) != 1 {
		t.Fatalf("want one UserPromptSubmit hook, got %+v", entries)
	}
	if cmd := entries[0].Hooks[0].Command; !strings.Contains(cmd, `_hook-state "test" "working" "user_prompt_submit"`) {
		t.Fatalf("UserPromptSubmit command = %q, want a distinct prompt receipt marker", cmd)
	}
}

func TestGenerateHooks_UsesWrapperPath(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "/Users/testuser/Applications/attn.app/Contents/MacOS/attn", nil)

	if !strings.Contains(hooks, "'/Users/testuser/Applications/attn.app/Contents/MacOS/attn' _hook-stop") {
		t.Error("hooks should include wrapper path in stop hook command")
	}
}

func TestGenerateHooks_DefaultsWrapperToAttn(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock", "", nil)

	if !strings.Contains(hooks, "'attn' _hook-stop") {
		t.Error("hooks should default wrapper path to 'attn'")
	}
}

func TestGenerateHooks_EnvBlock(t *testing.T) {
	decode := func(t *testing.T, content string) map[string]any {
		t.Helper()
		var parsed map[string]any
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		return parsed
	}

	t.Run("carries the supplied env", func(t *testing.T) {
		parsed := decode(t, Generate("test", "/tmp/test.sock", "/tmp/attn", map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "128000"}))
		env, ok := parsed["env"].(map[string]any)
		if !ok {
			t.Fatalf("env block missing: %#v", parsed)
		}
		if got := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "128000" {
			t.Fatalf("env cap = %v, want \"128000\"", got)
		}
	})

	t.Run("omits an empty env", func(t *testing.T) {
		if _, present := decode(t, Generate("test", "/tmp/test.sock", "/tmp/attn", nil))["env"]; present {
			t.Error("nil env should not emit an env block")
		}
		if _, present := decode(t, Generate("test", "/tmp/test.sock", "/tmp/attn", map[string]string{}))["env"]; present {
			t.Error("empty env should not emit an env block")
		}
	})
}

func TestGenerateCodexConfigOverrides_UsesStableEnvBasedCommands(t *testing.T) {
	overrides := GenerateCodexConfigOverrides("session-1", "/tmp/attn.sock", "/tmp/attn", Launch{WorkspaceContextPath: "/tmp/context.md"})
	joined := strings.Join(overrides, "\n")
	for _, expected := range []string{
		`shell_environment_policy.set.ATTN_SESSION_ID="session-1"`,
		`shell_environment_policy.set.ATTN_WRAPPER_PATH="/tmp/attn"`,
		`shell_environment_policy.set.ATTN_SOCKET_PATH="/tmp/attn.sock"`,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("codex overrides missing stable tool environment %q: %q", expected, joined)
		}
	}

	if !strings.Contains(joined, "hooks.SessionStart=") {
		t.Fatal("codex overrides should include SessionStart hook")
	}
	if !strings.Contains(joined, "_hook-session-start") {
		t.Fatal("codex overrides should sync session id on start")
	}
	if !strings.Contains(joined, `hooks.SessionStart=[{ matcher = "startup|resume|clear|compact"`) {
		t.Fatal("codex SessionStart hook should run after context resets")
	}
	if strings.Contains(joined, "_hook-session-start session-1") ||
		strings.Contains(joined, "_hook-state working session-1") {
		t.Fatal("codex hook commands should read identity from their stable environment")
	}
	if !strings.Contains(joined, "features.hooks=true") {
		t.Fatal("codex overrides should enable hooks for attn-managed sessions")
	}
	if !strings.Contains(joined, "features.terminal_resize_reflow=true") {
		t.Fatal("codex overrides should enable terminal resize reflow for embedded sessions")
	}
	if !strings.Contains(joined, "hooks.PreToolUse=") {
		t.Fatal("codex overrides should include PreToolUse hook")
	}
	if !strings.Contains(joined, `"/<session-flags>/config.toml:pre_tool_use:0:0" = { trusted_hash =`) {
		t.Fatal("codex overrides should trust attn-managed PreToolUse hook")
	}
	if !strings.Contains(joined, `hooks.state={`) ||
		!strings.Contains(joined, `"/<session-flags>/config.toml:session_start:0:0" = { trusted_hash =`) {
		t.Fatal("codex overrides should trust attn-managed session flag hooks")
	}
	if !strings.Contains(joined, "developer_instructions=") ||
		!strings.Contains(joined, "/tmp/context.md") {
		t.Fatal("codex overrides should inject workspace context as developer instructions")
	}
}

func TestGenerateCodexConfigOverrides_OmitsEmptySocketButKeepsSessionIdentity(t *testing.T) {
	overrides := strings.Join(GenerateCodexConfigOverrides("session-2", "", "", Launch{}), "\n")
	if !strings.Contains(overrides, `shell_environment_policy.set.ATTN_SESSION_ID="session-2"`) ||
		!strings.Contains(overrides, `shell_environment_policy.set.ATTN_WRAPPER_PATH="attn"`) {
		t.Fatalf("codex overrides dropped required attn identity: %q", overrides)
	}
	if strings.Contains(overrides, "ATTN_SOCKET_PATH") {
		t.Fatalf("codex overrides should omit an empty socket path: %q", overrides)
	}
}

func TestAgentInstructionsComposition(t *testing.T) {
	workflow := WorkflowTriggerGuidance()
	context := WorkspaceContextGuidance("/tmp/context.md")

	if got := AgentInstructions("", false); got != "" {
		t.Fatalf("AgentInstructions(empty, false) = %q, want empty", got)
	}

	contextOnly := AgentInstructions("/tmp/context.md", false)
	if contextOnly != context {
		t.Fatalf("context-only instructions = %q, want %q", contextOnly, context)
	}
	if strings.Contains(contextOnly, "hypercode") {
		t.Fatalf("context-only instructions leaked workflow guidance: %q", contextOnly)
	}

	workflowOnly := AgentInstructions("", true)
	if workflowOnly != workflow {
		t.Fatalf("workflow-only instructions = %q, want %q", workflowOnly, workflow)
	}

	both := AgentInstructions("/tmp/context.md", true)
	if want := strings.Join([]string{context, workflow}, "\n\n"); both != want {
		t.Fatalf("combined instructions = %q, want %q", both, want)
	}
}

func TestLaunchInstructionsGateGardenGuidance(t *testing.T) {
	if got := (Launch{Garden: true}).Instructions(); got != GardenGuidance {
		t.Fatalf("bare home launch = %q, want exact garden guidance", got)
	}

	withoutGarden := Launch{WorkspaceContextPath: "/tmp/context.md"}.Instructions()
	if strings.Contains(withoutGarden, GardenGuidance) {
		t.Fatalf("launch without home flag carried garden guidance:\n%s", withoutGarden)
	}

	withGarden := Launch{WorkspaceContextPath: "/tmp/context.md", Garden: true}.Instructions()
	if want := strings.Join([]string{WorkspaceContextGuidance("/tmp/context.md"), GardenGuidance}, "\n\n"); withGarden != want {
		t.Fatalf("home launch instructions differ: %q", withGarden)
	}

	chief := Launch{NotebookRoot: "/tmp/notebook", Garden: true}.Instructions()
	if !strings.Contains(chief, "You are the chief of staff") || !strings.Contains(chief, GardenGuidance) {
		t.Fatalf("home chief launch dropped a guidance block:\n%s", chief)
	}
}

func TestGardenGuidanceRequiresReadingUpdateNotificationsWithoutActing(t *testing.T) {
	for _, want := range []string{
		"run the suggested command to read it",
		"Reading acknowledges the update",
		"does not authorize or require acting on the update",
		"Only act or interrupt the user when attention is genuinely needed",
	} {
		if !strings.Contains(GardenGuidance, want) {
			t.Fatalf("garden guidance dropped %q:\n%s", want, GardenGuidance)
		}
	}
}

func TestGenerateCodexConfigOverrides_InjectsWorkflowGuidanceWhenEnabled(t *testing.T) {
	off := strings.Join(GenerateCodexConfigOverrides("s", "/sock", "/attn", Launch{WorkspaceContextPath: "/tmp/context.md"}), "\n")
	if strings.Contains(off, "hypercode") {
		t.Fatalf("workflow guidance injected while disabled: %q", off)
	}

	on := strings.Join(GenerateCodexConfigOverrides("s", "/sock", "/attn", Launch{WorkspaceContextPath: "/tmp/context.md", InjectWorkflow: true}), "\n")
	if !strings.Contains(on, "developer_instructions=") {
		t.Fatal("enabled overrides dropped developer_instructions")
	}
	if !strings.Contains(on, "hypercode") {
		t.Fatalf("enabled overrides missing workflow guidance: %q", on)
	}
	// The workspace context still rides alongside the workflow guidance.
	if !strings.Contains(on, "/tmp/context.md") {
		t.Fatalf("enabled overrides dropped the workspace context: %q", on)
	}

	// Workflow guidance is injected even without a workspace checkout.
	noCtx := strings.Join(GenerateCodexConfigOverrides("s", "/sock", "/attn", Launch{InjectWorkflow: true}), "\n")
	if !strings.Contains(noCtx, "developer_instructions=") || !strings.Contains(noCtx, "hypercode") {
		t.Fatalf("workflow guidance not injected without a checkout: %q", noCtx)
	}
}

func TestWorkspaceContextSessionStartOutputWrapsGuidance(t *testing.T) {
	raw := WorkspaceContextSessionStartOutput("/tmp/context.md")
	var output sessionStartHookOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		t.Fatalf("WorkspaceContextSessionStartOutput returned invalid JSON: %v", err)
	}
	if output.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hook event = %q", output.HookSpecificOutput.HookEventName)
	}
	// Non-chief agents are NOT nudged to journal: the SessionStart fallback carries
	// only the workspace-context guidance, with no journaling directive appended.
	want := WorkspaceContextGuidance("/tmp/context.md")
	if output.HookSpecificOutput.AdditionalContext != want {
		t.Fatal("hook output should carry only the workspace context guidance")
	}
}

func TestSessionStartOutputJoinsContextBlocks(t *testing.T) {
	raw := SessionStartOutput("workspace", "", "  garden  ")
	var output sessionStartHookOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		t.Fatalf("SessionStartOutput returned invalid JSON: %v", err)
	}
	if got, want := output.HookSpecificOutput.AdditionalContext, "workspace\n\ngarden"; got != want {
		t.Fatalf("additional context = %q, want %q", got, want)
	}
	if got := SessionStartOutput("", "  "); got != "" {
		t.Fatalf("empty SessionStartOutput = %q, want empty", got)
	}
}

func TestChiefGuidanceEmptyWithoutRoot(t *testing.T) {
	if got := ChiefGuidance(""); got != "" {
		t.Fatalf("ChiefGuidance(\"\") = %q, want empty", got)
	}
	if got := ChiefGuidance("   "); got != "" {
		t.Fatalf("ChiefGuidance(whitespace) = %q, want empty", got)
	}
}

func TestChiefGuidanceLeavesGardenToLaunch(t *testing.T) {
	guidance := ChiefGuidance("/tmp/notebook")
	for _, want := range []string{
		"End your turn after delegating",
		"Never park a blocking Monitor on attn activity",
		"attn seed show <seed-id>",
		"Record the delegation in the journal",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("chief guidance dropped %q:\n%s", want, guidance)
		}
	}
	if strings.Contains(guidance, GardenGuidance) || strings.Contains(guidance, "attn seed prime") {
		t.Fatalf("chief guidance duplicated the launch-owned garden block:\n%s", guidance)
	}
	for _, removed := range []string{
		"When you delegate, attn plants a seed",
		"Track work in seeds",
		"attn seed ls",
		"attn seed ready",
		"attn seed tend",
		"attn seed harvest",
	} {
		if strings.Contains(guidance, removed) {
			t.Fatalf("chief guidance kept removed garden copy %q:\n%s", removed, guidance)
		}
	}
}

// A woken member's crew block rides beside everything else a launch injects, and
// comes last — it is the most specific thing this session is.
func TestLaunchInstructionsCarryTheCrewPrimingLast(t *testing.T) {
	const block = "You are **Trellis**, a crew member of this attn home."

	launch := Launch{
		WorkspaceContextPath: "/tmp/context.md",
		Garden:               true,
		Crew:                 block,
	}.Instructions()

	if !strings.Contains(launch, GardenGuidance) {
		t.Fatalf("the crew block replaced the launch guidance:\n%s", launch)
	}
	if !strings.HasSuffix(strings.TrimSpace(launch), strings.TrimSpace(block)) {
		t.Fatalf("the crew block is not the last thing injected:\n%s", launch)
	}

	bare := Launch{WorkspaceContextPath: "/tmp/context.md"}.Instructions()
	if strings.Contains(bare, "crew member of this attn home") {
		t.Fatalf("a launch that woke nobody carried a crew block:\n%s", bare)
	}
}
