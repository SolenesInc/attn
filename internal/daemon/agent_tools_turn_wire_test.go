package daemon_test

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type sessionTools struct {
	w       *world
	app     *testworld.Peer
	session string
	agent   agentToolsCaller
	shapes  *toolShapes
}

func newSessionTools(t *testing.T, settings ...string) sessionTools {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	for _, key := range append([]string{"agent_tools_enabled"}, settings...) {
		setSetting(t, app, key, "true")
	}
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	return sessionTools{w: w, app: app, session: session, agent: agent, shapes: newToolShapes(t, agent, listOfferedTools(t, agent))}
}

func structuredAs(t *testing.T, result *mcp.CallToolResult, into any) {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err == nil {
		err = json.Unmarshal(data, into)
	}
	if err != nil {
		t.Fatalf("decoding %s: %v", resultText(result), err)
	}
}

func TestTheTurnToolsOpenAndSettleTheCallersTurn(t *testing.T) {
	s := newSessionTools(t)
	for _, step := range []struct {
		tool string
		want protocol.AgentTurnOutcome
	}{
		{"turn_open", protocol.AgentTurnOutcomeOpened},
		{"turn_settle", protocol.AgentTurnOutcomeSettled},
	} {
		var turn protocol.AgentTurnResult
		structuredAs(t, s.shapes.call(step.tool, map[string]any{}), &turn)
		if turn.Outcome != step.want {
			t.Errorf("%s answered %q, want %q", step.tool, turn.Outcome, step.want)
		}
	}
	s.shapes.finish("turn_")
}
