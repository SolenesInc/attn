package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

type fakeRunner struct {
	calls  []agentdriver.HeadlessTaskRequest
	behave func(call int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error)
}

func (f *fakeRunner) Run(_ context.Context, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
	n := len(f.calls)
	f.calls = append(f.calls, req)
	return f.behave(n, req)
}

func newTestDriverAgent(t *testing.T, runner headlessRunner, maxRetries int) *driverAgent {
	t.Helper()
	da, err := NewDriverAgent(DriverAgentOptions{
		Provider:       "codex",
		Executable:     "/bin/true",
		Model:          "test-model",
		RunTmpDir:      t.TempDir(),
		AttnExecutable: "/bin/true",
		MaxRetries:     maxRetries,
		Runner:         runner,
	})
	if err != nil {
		t.Fatalf("NewDriverAgent: %v", err)
	}
	return da
}

func writeValid(t *testing.T, path, payload string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write result file: %v", err)
	}
}

const testSchema = `{"type":"object","additionalProperties":false,"required":["answer"],"properties":{"answer":{"type":"string"}}}`

func ordForTest() OrdinalPath {
	ps := newPathStack()
	return ps.ordinalFor("test.js:1:1")
}

func TestDriverAgentHappySchemaPath(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		writeValid(t, req.ResultPath, `{"answer":"yes"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newTestDriverAgent(t, runner, 2)

	got, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "do it", Schema: json.RawMessage(testSchema)})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
	if !jsonStringEqual(string(got), `{"answer":"yes"}`) {
		t.Fatalf("result = %s, want {\"answer\":\"yes\"}", got)
	}
	req := runner.calls[0]
	if req.ToolName != "return_result" || req.MCPServerName == "" {
		t.Fatalf("schema path did not wire the result sink: %+v", req)
	}
	if !strings.Contains(req.Prompt, "return_result") {
		t.Fatalf("prompt missing the return_result instruction: %q", req.Prompt)
	}
}

func TestDriverAgentDetectMissingThenRetrySucceeds(t *testing.T) {
	runner := &fakeRunner{behave: func(call int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if call == 0 {
			return agentdriver.HeadlessTaskResult{}, nil
		}
		writeValid(t, req.ResultPath, `{"answer":"recovered"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newTestDriverAgent(t, runner, 2)

	got, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "do it", Schema: json.RawMessage(testSchema)})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %d, want 2 (one re-spawn)", len(runner.calls))
	}
	if !strings.Contains(runner.calls[1].Prompt, "previous attempt") {
		t.Fatalf("second prompt missing corrective text: %q", runner.calls[1].Prompt)
	}
	if !jsonStringEqual(string(got), `{"answer":"recovered"}`) {
		t.Fatalf("result = %s", got)
	}
}

func TestDriverAgentRetriesExhaustedResolvesNull(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newTestDriverAgent(t, runner, 2)

	got, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "do it", Schema: json.RawMessage(testSchema)})
	if err == nil {
		t.Fatalf("expected a terminal error (engine maps to null), got result %s", got)
	}
	if got != nil {
		t.Fatalf("terminal failure returned non-nil result: %s", got)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("runner calls = %d, want 3 (initial + 2 retries)", len(runner.calls))
	}
}

func TestDriverAgentNonZeroExitButFileWrittenIsSuccess(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		writeValid(t, req.ResultPath, `{"answer":"despite-exit"}`)
		return agentdriver.HeadlessTaskResult{Diagnostics: "headless agent process failed"}, errors.New("exit status 1")
	}}
	da := newTestDriverAgent(t, runner, 2)

	got, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "do it", Schema: json.RawMessage(testSchema)})
	if err != nil {
		t.Fatalf("Run error despite a written result: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1 (no retry needed)", len(runner.calls))
	}
	if !jsonStringEqual(string(got), `{"answer":"despite-exit"}`) {
		t.Fatalf("result = %s", got)
	}
}

func TestDriverAgentTerminalExitWithNoFileResolvesNull(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "headless agent authentication failed"},
			errors.New("exit status 1")
	}}
	da := newTestDriverAgent(t, runner, 1)

	got, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "do it", Schema: json.RawMessage(testSchema)})
	if err == nil {
		t.Fatalf("expected a terminal error (engine maps to null), got %s", got)
	}
	if got != nil {
		t.Fatalf("terminal failure returned non-nil result: %s", got)
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("diagnostics not surfaced: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %d, want 2 (initial + 1 retry)", len(runner.calls))
	}
}

func TestDriverAgentNoSchemaReturnsCapturedText(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if req.MCPServerName != "" || req.ToolName != "" {
			t.Fatalf("no-schema path wired an MCP server: %+v", req)
		}
		return agentdriver.HeadlessTaskResult{Text: "free text"}, nil
	}}
	da := newTestDriverAgent(t, runner, 2)

	got, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "just answer"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	var s string
	if err := json.Unmarshal(got, &s); err != nil {
		t.Fatalf("no-schema result is not a JSON string: %s (%v)", got, err)
	}
	if s != "free text" {
		t.Fatalf("captured text = %q, want \"free text\"", s)
	}
}

func TestDriverAgentNoSchemaTerminalFailureResolvesNull(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "headless agent process failed"},
			errors.New("exit status 1")
	}}
	da := newTestDriverAgent(t, runner, 2)

	got, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "answer"})
	if err == nil {
		t.Fatalf("expected a terminal error, got %s", got)
	}
	if got != nil {
		t.Fatalf("terminal failure returned non-nil result: %s", got)
	}
}

func TestDriverAgentThroughEngineSchemaPath(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if len(req.Schema) == 0 {
			t.Fatalf("schema did not reach the driver: %+v", req)
		}
		writeValid(t, req.ResultPath, `{"answer":"hello"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newTestDriverAgent(t, runner, 2)

	script := `
		const r = await agent("answer please", { schema: {
			type: "object", additionalProperties: false,
			required: ["answer"], properties: { answer: { type: "string" } }
		}});
		return r.answer;
	`
	eng := New(Config{Stub: da, WatchdogTimeout: 5 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("engine run error: %v (status=%s)", err, res.Status)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	if res.Value != "hello" {
		t.Fatalf("script result = %#v, want \"hello\"", res.Value)
	}
}

func TestDriverAgentThroughEngineResolvesNull(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{}, nil // never writes a result
	}}
	da := newTestDriverAgent(t, runner, 1)

	script := `
		const r = await agent("answer please", { schema: {
			type: "object", required: ["answer"], properties: { answer: { type: "string" } }
		}});
		return { isNull: r === null };
	`
	eng := New(Config{Stub: da, WatchdogTimeout: 5 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("engine run errored (agent failure must not reject): %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v, want completed", res.Status, res.Err)
	}
	obj, ok := res.Value.(map[string]interface{})
	if !ok || obj["isNull"] != true {
		t.Fatalf("missing-result agent() did not resolve null: %#v", res.Value)
	}
}

func newWritableTestDriverAgent(t *testing.T, runner headlessRunner, workingTree string, servers []agentdriver.MCPServerSpec) *driverAgent {
	t.Helper()
	da, err := NewDriverAgent(DriverAgentOptions{
		Provider:          "codex",
		Executable:        "/bin/true",
		Model:             "test-model",
		RunTmpDir:         t.TempDir(),
		AttnExecutable:    "/bin/true",
		MaxRetries:        2,
		Runner:            runner,
		WorkingTree:       workingTree,
		SessionMCPServers: servers,
	})
	if err != nil {
		t.Fatalf("NewDriverAgent: %v", err)
	}
	return da
}

func TestDriverAgentSchemaPathIsWritableWithCWDAndExtraServers(t *testing.T) {
	tree := t.TempDir()
	servers := []agentdriver.MCPServerSpec{{
		Name:         "session_tools",
		Command:      "/tmp/session-mcp",
		Args:         []string{"serve"},
		EnabledTools: []string{"do_thing"},
	}}
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		writeValid(t, req.ResultPath, `{"answer":"yes"}`)
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	da := newWritableTestDriverAgent(t, runner, tree, servers)

	if _, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "do it", Schema: json.RawMessage(testSchema)}); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	req := runner.calls[0]
	if req.Sandbox != "workspace-write" {
		t.Fatalf("schema path sandbox = %q, want workspace-write", req.Sandbox)
	}
	if req.CWD != tree {
		t.Fatalf("schema path CWD = %q, want working tree %q", req.CWD, tree)
	}
	if req.WorkDir == tree {
		t.Fatalf("scratch WorkDir must stay in the run temp dir, not the working tree: %q", req.WorkDir)
	}
	if len(req.ExtraMCPServers) != 1 || req.ExtraMCPServers[0].Name != "session_tools" {
		t.Fatalf("session MCP servers not threaded into ExtraMCPServers: %+v", req.ExtraMCPServers)
	}
	if req.ToolName != resultToolName || req.MCPServerName != "attn_workflow_result" {
		t.Fatalf("schema path no longer wires the result sink: %+v", req)
	}
	if !containsArg(req.MCPServerArgs, "--result-file") {
		t.Fatalf("result sink argv lost --result-file: %v", req.MCPServerArgs)
	}
}

func TestDriverAgentNoSchemaPathIsWritableWithCWDAndExtraServers(t *testing.T) {
	tree := t.TempDir()
	servers := []agentdriver.MCPServerSpec{{
		Name:         "session_tools",
		Command:      "/tmp/session-mcp",
		EnabledTools: []string{"do_thing"},
	}}
	runner := &fakeRunner{behave: func(_ int, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Text: "ok"}, nil
	}}
	da := newWritableTestDriverAgent(t, runner, tree, servers)

	if _, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "answer"}); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	req := runner.calls[0]
	if req.Sandbox != "workspace-write" {
		t.Fatalf("no-schema path sandbox = %q, want workspace-write", req.Sandbox)
	}
	if req.CWD != tree {
		t.Fatalf("no-schema path CWD = %q, want working tree %q", req.CWD, tree)
	}
	if len(req.ExtraMCPServers) != 1 || req.ExtraMCPServers[0].Name != "session_tools" {
		t.Fatalf("session MCP servers not threaded into ExtraMCPServers: %+v", req.ExtraMCPServers)
	}
	if req.MCPServerName != "" || req.ToolName != "" {
		t.Fatalf("no-schema path wired an MCP result sink: %+v", req)
	}
}

func TestDriverAgentCWDFallsBackToRunTmpDirWhenNoWorkingTree(t *testing.T) {
	runner := &fakeRunner{behave: func(_ int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if len(req.Schema) != 0 {
			writeValid(t, req.ResultPath, `{"answer":"yes"}`)
		}
		return agentdriver.HeadlessTaskResult{Text: "ok"}, nil
	}}
	da := newWritableTestDriverAgent(t, runner, "" /* no working tree */, nil)

	if _, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "answer"}); err != nil {
		t.Fatalf("no-schema Run error: %v", err)
	}
	if got := runner.calls[0].CWD; got != da.runTmpDir {
		t.Fatalf("no-schema CWD = %q, want runTmpDir %q", got, da.runTmpDir)
	}

	if _, err := da.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: "do it", Schema: json.RawMessage(testSchema)}); err != nil {
		t.Fatalf("schema Run error: %v", err)
	}
	if got := runner.calls[1].CWD; got != da.runTmpDir {
		t.Fatalf("schema CWD = %q, want runTmpDir %q", got, da.runTmpDir)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func jsonStringEqual(a, b string) bool {
	var av, bv any
	if json.Unmarshal([]byte(a), &av) != nil {
		return false
	}
	if json.Unmarshal([]byte(b), &bv) != nil {
		return false
	}
	an, _ := json.Marshal(av)
	bn, _ := json.Marshal(bv)
	return string(an) == string(bn)
}
