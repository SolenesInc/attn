package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
)

func TestEveryAgentSessionAndStateToolAnswersWithTheShapeItsOutputSchemaDescribes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))

	shapes.call("state_explain", map[string]any{"id": session})
	shapes.call("list", map[string]any{})

	shapes.finish("state_", "list")

	unknown := agent.mustCall(t, "state_explain", map[string]any{"id": "no-such-session"})
	if text := resultText(unknown); !unknown.IsError || text != `session_not_found: no session "no-such-session"; pass a full session id`+"\nFix: Pass the full id of a session this daemon holds." {
		t.Errorf("state_explain of a session that does not exist answered %q, want session_not_found with the catalog's fix", text)
	}
}
