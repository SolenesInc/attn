package daemon_test

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentPointsAtTheAppWithTheCalloutTools(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))

	unseen := narrowed[protocol.CalloutAnchorsResult](t, shapes.call("callout_anchors", map[string]any{}).StructuredContent)
	if unseen.AppShowingProfile || len(unseen.Anchors) != 11 {
		t.Errorf("callout_anchors with no app showing the profile = %+v, want the directory and app_showing_profile false", unseen)
	}
	refused := agent.mustCall(t, "callout_show", map[string]any{"steps": []any{map[string]any{"anchor": "garden", "text": "Work lives here"}}})
	if text := resultText(refused); !refused.IsError || !strings.HasPrefix(text, "app_not_showing_profile: ") || !strings.Contains(text, "\nFix: ") {
		t.Errorf("callout_show with no app = %q, want the app_not_showing_profile refusal with its fix", text)
	}

	target := calloutAppPeer(w.World, "", browserHostToken)
	selectProfile(target, app.SelectedProfile())
	answered := make(chan *mcp.CallToolResult, 1)
	go func() { answered <- shapes.call("callout_anchors", map[string]any{}) }()
	asked := testworld.Await[protocol.CalloutAnchorsRequestMessage](target, protocol.EventCalloutAnchorsRequest, nil)
	target.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: asked.RequestID, OnScreen: []protocol.CalloutAnchor{protocol.CalloutAnchorGarden}})
	listed := narrowed[protocol.CalloutAnchorsResult](t, (<-answered).StructuredContent)
	if !listed.AppShowingProfile || listed.Anchors[3].Name != "garden" || !protocol.Deref(listed.Anchors[3].OnScreen) || protocol.Deref(listed.Anchors[0].OnScreen) {
		t.Errorf("callout_anchors = %+v, want garden alone on screen", listed)
	}

	go func() {
		answered <- shapes.call("callout_show", map[string]any{"steps": []any{
			map[string]any{"anchor": "garden", "text": "Work lives here"},
			map[string]any{"anchor": "agent:" + session[:8], "text": "Enter goes here"},
		}})
	}()
	shown := testworld.Await[protocol.CalloutShowRequestMessage](target, protocol.EventCalloutShowRequest, nil)
	if len(shown.Steps) != 2 || protocol.Deref(shown.Steps[1].SessionID) != protocol.SessionID(session) {
		t.Fatalf("app request = %+v, want both steps with the agent resolved to the caller", shown)
	}
	target.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: shown.RequestID, OnScreen: []protocol.CalloutAnchor{protocol.CalloutAnchorGarden}})
	if got := narrowed[protocol.CalloutShowResult](t, (<-answered).StructuredContent); got.Steps != 2 {
		t.Errorf("callout_show = %+v, want 2 steps shown", got)
	}

	unknown := agent.mustCall(t, "callout_show", map[string]any{"steps": []any{map[string]any{"anchor": "nowhere", "text": "x"}}})
	if text := resultText(unknown); !unknown.IsError || !strings.HasPrefix(text, `unknown_anchor: unknown anchor "nowhere"`) || !strings.Contains(text, "\nFix: ") {
		t.Errorf("callout_show of an unknown anchor = %q, want the unknown_anchor refusal with a fix", text)
	}
	shapes.finish("callout_")
}
