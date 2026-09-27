package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{"result": schemaCallInstruction, "retry": correctiveInstruction}
	runner := &fakeRunner{behave: func(call int, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		out[fmt.Sprint("request/", call)] = req.Prompt
		if call > 0 {
			writeValid(t, req.ResultPath, `{"answer":"recovered"}`)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}}
	driver := newTestDriverAgent(t, runner, 2)
	_, err := driver.Run(context.Background(), AgentCall{Ordinal: ordForTest(), Prompt: " Task {{literal}} λ\nnext ", Schema: json.RawMessage(testSchema)})
	if err != nil {
		t.Fatal(err)
	}
	prompttest.Equal(t, "workflow", out)
}

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
