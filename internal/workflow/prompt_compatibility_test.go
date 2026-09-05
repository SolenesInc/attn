package workflow

import (
	"context"
	"encoding/json"
	"fmt"
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
