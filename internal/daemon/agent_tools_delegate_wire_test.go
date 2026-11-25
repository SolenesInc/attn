package daemon_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

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
	if shown := call("delegate_roles_show", map[string]any{"role": "builder"}); !strings.Contains(fmt.Sprint(shown["role"]), "attn-workflow") {
		t.Errorf("delegate_roles_show for the maintained builder answered role %v, want its instructions filled in", shown["role"])
	}
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
	if relative["state"] != "completed" || relative["result"] == nil {
		t.Errorf("delegate answered %v, want the delegation completed with its result: its agent took its first turn", relative)
	}
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

func TestADelegateCallerReadsAndDelegatesNothingOfAnotherProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	home := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	seed := plantForTools(t, w)
	var started protocol.DelegationOperation
	if err := w.Client().RunCommand("delegate", protocol.SessionID(home), map[string]any{"brief": "Default profile work", "cwd": w.Path("shop"), "model": "default", "request_id": "default-profile-1"}, &started); err != nil {
		t.Fatal(err)
	}

	sideApp := w.AppOn(createProfile(app, "Side").ID)
	side := w.Spawn(sideApp, fakeagent.Claude, w.Path("side"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, sideApp.Terminal(side))}
	for _, tc := range []struct {
		tool, command string
		args          map[string]any
	}{
		{"delegate_status", "delegate status", map[string]any{"id": "default-profile-1"}},
		{"delegate_status", "delegate status", map[string]any{"id": started.OperationID}},
		{"delegate", "delegate", map[string]any{"seed": seed, "cwd": w.Path("side"), "model": "default"}},
	} {
		if refused := agent.mustCall(t, tc.tool, tc.args); !refused.IsError || !strings.Contains(resultText(refused), `belongs to profile "Default"`) {
			t.Errorf("%s %v from the Side profile's session answered %s, want a refusal naming the Default profile", tc.tool, tc.args, resultText(refused))
		}
		var answer map[string]any
		if err := w.Client().RunCommand(tc.command, protocol.SessionID(side), tc.args, &answer); err == nil || !strings.Contains(err.Error(), `belongs to profile "Default"`) {
			t.Errorf("run_command %s %v as the Side profile's session answered %v, %v, want a refusal naming the Default profile", tc.command, tc.args, answer, err)
		}
	}
}

func TestADelegateToolCallStopsWaitingForASilentAgentAndTheStartGoesOn(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		caller := w.bubbleClaude(t, w.App(), "shop")
		args := map[string]any{"brief": "Say hello", "cwd": w.Path("shop"), "model": "default", "request_id": "silent-agent"}

		asked := time.Now()
		var operation protocol.DelegationOperation
		if err := caller.cli.RunCommand("delegate", protocol.SessionID(caller.id), args, &operation); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(asked); waited < 15*time.Second || waited > 30*time.Second {
			t.Errorf("delegate answered after %s, want a wait under the 30s a harness gives a tool call", waited)
		}
		if operation.State != protocol.DelegationOperationStatePreparing || operation.Result != nil {
			t.Errorf("delegate answered %+v, want the operation still preparing", operation)
		}

		w.bootBubbleClaude(t, string(operation.SessionID))
		if err := caller.cli.RunCommand("delegate status", protocol.SessionID(caller.id), map[string]any{"id": "silent-agent"}, &operation); err != nil {
			t.Fatal(err)
		}
		if operation.State != protocol.DelegationOperationStateCompleted || operation.Result == nil || operation.Result.FirstTurnAt == nil {
			t.Errorf("once the agent took its first turn the operation is %+v, want it completed", operation)
		}
	})
}

func TestADelegateToolCallNamesTheFolderTrustQuestionItsDelegateIsStoppedAt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		caller := w.bubbleClaude(t, w.App(), "shop")
		answered := make(chan protocol.DelegationOperation, 1)
		go func() {
			var operation protocol.DelegationOperation
			args := map[string]any{"brief": "Say hello", "cwd": w.Path("shop"), "model": "default", "request_id": "untrusted-folder", "name": "hello"}
			if err := caller.cli.RunCommand("delegate", protocol.SessionID(caller.id), args, &operation); err != nil {
				t.Errorf("delegating: %v", err)
			}
			answered <- operation
		}()
		w.advance(5 * time.Second)
		delegate := sessionLabeled(t, w, "hello")
		w.terminal(string(delegate.ID)).PaintScreen("Quick safety check\r\n❯ No, exit\r\n  " + folderTrustChoice)

		operation := <-answered
		if operation.State != protocol.DelegationOperationStatePreparing {
			t.Errorf("delegate answered %+v, want the operation still preparing", operation)
		}
		for _, want := range []string{"folder-trust question", shortID(delegate.ID)} {
			if !strings.Contains(operation.Progress, want) {
				t.Errorf("the answer's progress is %q, want it to name %q", operation.Progress, want)
			}
		}
	})
}

func TestADelegationThatFailsAfterItWasAcceptedAnswersFailedAndReachesTheCallersInbox(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	repo := w.Path("repo")
	gitRepo(t, repo)

	answered := agent.mustCall(t, "delegate", map[string]any{"brief": "Work in a repository without a checkout", "cwd": repo, "model": "default", "request_id": "fails-after-acceptance"})
	if answered.IsError {
		t.Fatalf("delegate refused before acceptance: %s", resultText(answered))
	}
	var operation protocol.DelegationOperation
	data, _ := json.Marshal(answered.StructuredContent)
	if err := json.Unmarshal(data, &operation); err != nil {
		t.Fatal(err)
	}
	if operation.State != protocol.DelegationOperationStateFailed || operation.Failure == nil {
		t.Fatalf("delegate answered %+v, want the failed delegation: it needs a checkout in a repository", operation)
	}
	items := readInbox(t, w.Client(), session, 10).Items
	if len(items) != 1 || items[0].Kind != "maintenance_prompt" || !strings.Contains(items[0].Content, "fails-after-acceptance") || !strings.Contains(items[0].Content, "git checkout requires") {
		t.Fatalf("the caller's inbox holds %+v, want one notice naming the request and why it failed", items)
	}
}
