package daemon_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryDelegateToolAnswersWithTheShapeItsOutputSchemaDescribes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := listOfferedTools(t, agent)

	called := map[string]bool{}
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		called[tool] = true
		result := agent.mustCall(t, tool, args)
		if result.IsError {
			t.Fatalf("%s %v refused: %s", tool, args, resultText(result))
		}
		var instance map[string]any
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &instance); err != nil {
			t.Fatal(err)
		}
		schema, ok := tools[tool]
		if !ok || schema == nil {
			t.Fatalf("%s is not offered with an outputSchema", tool)
		}
		if err := schema.Validate(instance); err != nil {
			t.Errorf("%s answered outside its outputSchema: %v", tool, err)
		}
		return instance
	}

	call("delegate_roles_install", map[string]any{"message": "Set up the roles"})
	call("delegate_roles_add", map[string]any{"target": "custom", "name": "Custom", "agent": "claude", "model": "opus", "message": "a custom role"})
	call("delegate_roles_add", map[string]any{"target": "custom/fast", "when": "small changes", "model": "haiku"})
	call("delegate_roles_set", map[string]any{"target": "custom/fast", "default": true, "icon": ""})
	call("delegate_roles_set", map[string]any{"fallback": true, "agent": "claude", "model": "sonnet"})
	call("delegate_roles_copy", map[string]any{"role": "builder", "new_role": "mine"})
	call("delegate_roles_rm", map[string]any{"target": "mine"})
	call("delegate_roles_disable", map[string]any{"role": "custom"})
	call("delegate_roles_enable", map[string]any{"role": "custom"})
	call("delegate_roles_show", map[string]any{"role": "builder"})
	shown := call("delegate_roles_show", map[string]any{})
	call("delegate_roles_apply", map[string]any{"table": shown, "message": "round trip"})
	call("delegate_roles_history", map[string]any{"limit": 3})
	call("delegate_roles_rollback", map[string]any{})
	offered := call("delegate_roles", map[string]any{})
	if roles, _ := offered["roles"].([]any); len(roles) != 1 || offered["text"] != nil {
		t.Errorf("delegate_roles answered %v, want only the custom role, the one whose model is ready, and no CLI text", offered)
	}
	if refused := agent.mustCall(t, "delegate_roles", map[string]any{"render": true}); !refused.IsError {
		t.Errorf("delegate_roles took render, the arg only the CLI fills: %s", resultText(refused))
	}

	relative := call("delegate", map[string]any{"brief": "Work in the session's folder", "cwd": ".", "model": "default", "request_id": "relative-1", "name": "relative"})
	retried := call("delegate", map[string]any{"brief": "Work in the session's folder", "cwd": ".", "model": "default", "request_id": "relative-1", "name": "relative"})
	if retried["operation_id"] != relative["operation_id"] || retried["session_id"] != relative["session_id"] {
		t.Errorf("a retry with the same request_id answered %v, want the first call's operation %v", retried, relative)
	}
	absolute := call("delegate", map[string]any{"brief": "Work in the session's folder", "cwd": w.Path("shop"), "model": "default", "request_id": "absolute-1", "name": "absolute"})
	for _, started := range []map[string]any{relative, absolute} {
		testworld.AwaitSession(app, started["session_id"].(string), func(s protocol.Session) bool { return s.Directory == w.Path("shop") })
	}
	if status := call("delegate_status", map[string]any{"id": "relative-1"}); status["session_id"] != relative["session_id"] {
		t.Errorf("delegate_status answered %v, want the relative delegation's session %v", status, relative["session_id"])
	}

	var uncalled []string
	for tool := range tools {
		if strings.HasPrefix(tool, "delegate_") && !called[tool] {
			uncalled = append(uncalled, tool)
		}
	}
	sort.Strings(uncalled)
	if len(uncalled) > 0 {
		t.Errorf("offered delegate tools this test never called: %v", uncalled)
	}
}
