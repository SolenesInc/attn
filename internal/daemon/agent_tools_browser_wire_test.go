package daemon_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentDrivesTheBrowserTileWithTheBrowserTools(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := listOfferedTools(t, agent)
	shapes := newToolShapes(t, agent, tools)
	if _, offered := tools["browser_wait"]; offered {
		t.Error("browser_wait is offered as a tool, but it holds the call past a harness's limit")
	}
	host := browserHostPeer(w, "", browserHostToken)

	if opened := narrowed[protocol.BrowserOpenResult](t, shapes.call("browser_open", map[string]any{"url": "http://localhost:3000"}).StructuredContent); opened.URL != "http://localhost:3000" {
		t.Errorf("browser_open = %+v, want the URL it opened", opened)
	}

	ask := func(tool string, args map[string]any, wantActions []string, data string) *mcp.CallToolResult {
		t.Helper()
		answered := make(chan *mcp.CallToolResult, 1)
		go func() { answered <- shapes.call(tool, args) }()
		for _, action := range wantActions {
			request := testworld.Await[protocol.BrowserControlRequestMessage](host, protocol.EventBrowserControlRequest, nil)
			if request.Action != action {
				t.Errorf("%s asked the host for %q, want %q", tool, request.Action, action)
			}
			host.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: request.RequestID, Success: true, Data: protocol.Ptr(data)})
		}
		return <-answered
	}
	for _, tc := range []struct {
		tool    string
		args    map[string]any
		actions []string
	}{
		{"browser_snapshot", map[string]any{}, []string{"snapshot"}},
		{"browser_find", map[string]any{"using": "role", "value": "textbox", "name": "Search"}, []string{"find_element"}},
		{"browser_click", map[string]any{"element": "attn-element-1"}, []string{"click_element"}},
		{"browser_type", map[string]any{"element": "attn-element-1", "text": "query"}, []string{"clear_element", "send_keys_to_element"}},
		{"browser_press", map[string]any{"text": "Enter"}, []string{"perform_actions"}},
		{"browser_scroll", map[string]any{"y": 200}, []string{"perform_actions"}},
		{"browser_back", map[string]any{}, []string{"back"}},
		{"browser_forward", map[string]any{}, []string{"forward"}},
		{"browser_reload", map[string]any{}, []string{"reload"}},
		{"browser_cookies", map[string]any{}, []string{"get_all_cookies"}},
		{"browser_command", map[string]any{"action": "get_title"}, []string{"get_title"}},
	} {
		if got := narrowed[protocol.BrowserResult](t, ask(tc.tool, tc.args, tc.actions, "answer of "+tc.tool).StructuredContent); got.Data != "answer of "+tc.tool {
			t.Errorf("%s = %+v, want the host's answer", tc.tool, got)
		}
	}

	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG fake"))
	absolute := filepath.Join(w.Path("shop"), "shot.png")
	for _, given := range []string{"shot.png", absolute, "shot.png"} {
		saved := narrowed[protocol.BrowserFileResult](t, ask("browser_screenshot", map[string]any{"path": given}, []string{"screenshot"}, png).StructuredContent)
		info, err := os.Stat(absolute)
		if saved.Path != absolute || err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("browser_screenshot of %q = %+v (%v), want %s written readable only by its owner", given, saved, err, absolute)
		}
	}
	if saved := narrowed[protocol.BrowserFileResult](t, ask("browser_pdf", map[string]any{}, []string{"print_page"}, png).StructuredContent); saved.Path != filepath.Join(w.Path("shop"), "attn-browser.pdf") {
		t.Errorf("browser_pdf with no path = %+v, want attn-browser.pdf in the session's folder", saved)
	}
	shapes.finish("browser_")
}
