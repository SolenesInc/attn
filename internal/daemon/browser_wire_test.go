package daemon_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const browserHostToken = "browser-host-secret"

func TestBrowserCommandsRefuseWhatTheHostCannotRun(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()

	for _, url := range []string{"", "file:///tmp/index.html", "javascript:alert(1)", "https://"} {
		if err := cli.OpenBrowser(url, ""); err == nil {
			t.Errorf("attn browser open %q succeeded, want it refused", url)
		}
	}
	for _, tc := range []struct {
		name, action, params, want string
	}{
		{"an unknown action", "submit", "", "unsupported action"},
		{"a timeout above the maximum", "snapshot", `{"timeout":120001}`, "timeout cannot exceed"},
		{"params that are not an object", "find_element", "null", "JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cli.BrowserCommand(tc.action, tc.params, "", "", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("browser %s = %v, want it refused naming %q", tc.action, err, tc.want)
			}
		})
	}
	for _, action := range []string{"snapshot", "click", "type", "reload", "navigate", "screenshot", "find_element", "perform_actions", "get_all_cookies", "print_page", "wait_for"} {
		if _, err := cli.BrowserCommand(action, "", "#query", "https://example.com", ""); err == nil || !strings.Contains(err.Error(), "no workspace selected") {
			t.Errorf("browser %s = %v, want it accepted up to resolving its target workspace", action, err)
		}
	}
}

func TestAttnBrowserOpenDocksIntoTheSelectedWorkspace(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	host := browserHostPeer(w, "tauri://localhost", browserHostToken)
	shop, shopWorkspace, shopPane := w.RequestSpawn(app, workspaceShell, w.Path("shop"))
	browserAppSelects(app, protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: shop.ID})

	if err := cli.OpenBrowser("  http://localhost:3000/path  ", ""); err != nil {
		t.Fatalf("attn browser open: %v", err)
	}
	awaitBrowserTile(t, app, shopWorkspace, "http://localhost:3000/path")

	if err := cli.OpenBrowser("http://localhost:3000/path", ""); err != nil {
		t.Fatalf("attn browser open again: %v", err)
	}
	awaitBrowserNavigation(host, shopWorkspace, "http://localhost:3000/path")
	if leaves := workspaceLayoutTree(t, workspaceLayoutNow(t, w, shopWorkspace)).leafIDs(); !slices.Equal(leaves, []string{shopPane, "tile-browser"}) {
		t.Errorf("after opening the same URL the layout holds %v, want the pane and one browser tile", leaves)
	}

	closeBrowserWorkspacePane(t, app, shopWorkspace, shopPane)
	browserAppSelects(app, protocol.WorkspaceSelectedMessage{Cmd: protocol.CmdWorkspaceSelected, WorkspaceID: shopWorkspace})
	if err := cli.OpenBrowser("https://example.com/retargeted", ""); err != nil {
		t.Fatalf("attn browser open in the tile-only workspace: %v", err)
	}
	awaitBrowserTile(t, app, shopWorkspace, "https://example.com/retargeted")
	awaitBrowserNavigation(host, shopWorkspace, "https://example.com/retargeted")

	docs, docsWorkspace, docsPane := w.RequestSpawn(app, workspaceShell, w.Path("docs"))
	docked := workspaceLayoutAction(app, protocol.WorkspaceLayoutDockTileMessage{
		Cmd: protocol.CmdWorkspaceLayoutDockTile, WorkspaceID: docsWorkspace, AnchorPaneID: docsPane, Edge: protocol.WorkspaceLayoutDockEdgeRight,
		TileID: "tile-notes", TileKind: "markdown", TileParams: protocol.Ptr(w.Path("docs", "notes.md")),
	}, protocol.CmdWorkspaceLayoutDockTile, docsWorkspace)
	if !docked.Success {
		t.Fatalf("docking notes beside %s: %s", docs.ID, protocol.Deref(docked.Error))
	}
	closeBrowserWorkspacePane(t, app, docsWorkspace, docsPane)
	browserAppSelects(app, protocol.WorkspaceSelectedMessage{Cmd: protocol.CmdWorkspaceSelected, WorkspaceID: docsWorkspace})
	if err := cli.OpenBrowser("https://example.com", ""); err != nil {
		t.Fatalf("attn browser open beside the notes: %v", err)
	}
	if tiles := awaitBrowserTile(t, app, docsWorkspace, "https://example.com"); tiles["tile-notes"].TileKind != "markdown" {
		t.Errorf("tiles = %+v, want the browser docked beside the notes", tiles)
	}
}

func TestBrowserControlIsBrokeredToTheHostThatWasAsked(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop, shopWorkspace, _ := w.RequestSpawn(app, workspaceShell, w.Path("shop"))
	docs, docsWorkspace, _ := w.RequestSpawn(app, workspaceShell, w.Path("docs"))
	for _, session := range []string{shop.ID, docs.ID} {
		if err := cli.OpenBrowser("http://localhost:3000", session); err != nil {
			t.Fatalf("open a browser for %s: %v", session, err)
		}
	}
	browserAppSelects(app, protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: shop.ID})
	spoof := browserHostPeer(w, "tauri://localhost", browserHostToken)
	host := browserHostPeer(w, "http://tauri.localhost", browserHostToken)

	answered := make(chan browserControlAnswer, 1)
	go func() {
		data, err := w.Client().BrowserCommand("type", "", "#query", "browser text", "")
		answered <- browserControlAnswer{data, err}
	}()
	request := testworld.Await(host, protocol.EventBrowserControlRequest, func(r protocol.BrowserControlRequestMessage) bool { return r.Action == "type" })
	if request.WorkspaceID != shopWorkspace || protocol.Deref(request.Selector) != "#query" || protocol.Deref(request.Text) != "browser text" {
		t.Errorf("the host was asked %+v, want to type into #query in %s", request, shopWorkspace)
	}
	spoof.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: request.RequestID, Success: true, Data: protocol.Ptr("spoofed")})
	browserPeerCaughtUp(spoof)
	large := strings.Repeat("A", 2<<20)
	host.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: request.RequestID, Success: true, Data: protocol.Ptr(large)})
	if got := <-answered; got.err != nil || got.data != large {
		t.Errorf("the CLI got %d bytes (%v), want the host's %d-byte result and not the spoof's", len(got.data), got.err, len(large))
	}

	hub := w.App()
	hub.Send(protocol.BrowserControlMessage{Cmd: protocol.CmdBrowserControl, Action: "get_title", RequestID: protocol.Ptr("remote-request-1"), WorkspaceID: protocol.Ptr(docsWorkspace)})
	forwarded := testworld.Await(host, protocol.EventBrowserControlRequest, func(r protocol.BrowserControlRequestMessage) bool { return r.Action == "get_title" })
	if forwarded.WorkspaceID != docsWorkspace {
		t.Errorf("the hub's request reached the host for %s, want the named workspace %s", forwarded.WorkspaceID, docsWorkspace)
	}
	host.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: forwarded.RequestID, Success: true, Data: protocol.Ptr(`"Remote title"`)})
	response := testworld.Await(hub, protocol.EventBrowserControlResponse, func(r protocol.BrowserControlResponseMessage) bool { return r.RequestID == "remote-request-1" })
	if !response.Success || protocol.Deref(response.Data) != `"Remote title"` {
		t.Errorf("the hub got %+v, want the host's title", response)
	}

	ordinary := w.App()
	ordinary.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: shop.ID, Data: strings.Repeat("x", 1<<20)})
	if status := ordinary.Closed(); status.Code != websocket.StatusMessageTooBig {
		t.Errorf("an app peer sending a message past the command-sized limit was closed with %d, want %d", status.Code, websocket.StatusMessageTooBig)
	}
	exitWorkspaceShells(app, shop.ID, docs.ID)
}

func TestOnlyTheAppsOriginWithTheHostTokenBecomesTheBrowserHost(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop, _, _ := w.RequestSpawn(app, workspaceShell, w.Path("shop"))
	if err := cli.OpenBrowser("http://localhost:3000", shop.ID); err != nil {
		t.Fatalf("open a browser: %v", err)
	}
	browserAppSelects(app, protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: shop.ID})

	for _, tc := range []struct {
		instance, origin, token string
		host                    bool
	}{
		{"", "", browserHostToken, false},
		{"", "http://localhost:3000", browserHostToken, false},
		{"", "http://127.0.0.1:5173", browserHostToken, false},
		{"", "http://localhost:1420", browserHostToken, false},
		{"", "tauri://localhost", "wrong-secret", false},
		{"dev", "http://localhost:3000", browserHostToken, false},
		{"", "tauri://localhost", browserHostToken, true},
		{"", "http://tauri.localhost", browserHostToken, true},
		{"dev", "http://localhost:1420", browserHostToken, true},
	} {
		t.Run(tc.instance+" "+tc.origin+" "+tc.token, func(t *testing.T) {
			t.Setenv("ATTN_INSTANCE", tc.instance)
			candidate := browserHostPeer(w, tc.origin, tc.token)
			defer candidate.Close()
			answered := make(chan browserControlAnswer, 1)
			go func() {
				data, err := cli.BrowserCommand("snapshot", "", "", "", "")
				answered <- browserControlAnswer{data, err}
			}()
			if !tc.host {
				if got := <-answered; got.err == nil || !strings.Contains(got.err.Error(), "no in-app browser host is connected") {
					t.Errorf("with only this peer connected browser control = %q, %v; want no browser host", got.data, got.err)
				}
				return
			}
			request := testworld.Await(candidate, protocol.EventBrowserControlRequest, func(r protocol.BrowserControlRequestMessage) bool { return r.Action == "snapshot" })
			candidate.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: request.RequestID, Success: true, Data: protocol.Ptr("page")})
			if got := <-answered; got.err != nil || got.data != "page" {
				t.Errorf("browser control = %q, %v; want the host's answer", got.data, got.err)
			}
		})
	}
	exitWorkspaceShells(app, shop.ID)
}

type browserControlAnswer struct {
	data string
	err  error
}

func browserHostPeer(w *world, origin, token string) *testworld.Peer {
	w.T.Helper()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	p := w.Connect(protocol.ClientHelloMessage{
		Cmd:              protocol.CmdClientHello,
		ClientKind:       "tauri-app",
		Version:          "protocol-" + protocol.ProtocolVersion,
		Capabilities:     []string{protocol.CapabilityWorkspaceSessions, protocol.CapabilityBrowserHost},
		ClientToken:      protocol.Ptr(config.ClientToken()),
		BrowserHostToken: protocol.Ptr(token),
	}, header)
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}

func browserAppSelects(app *testworld.Peer, selection any) {
	app.T.Helper()
	app.Send(selection)
	browserPeerCaughtUp(app)
}

func browserPeerCaughtUp(p *testworld.Peer) {
	p.T.Helper()
	testworld.Request(p, protocol.GetPresentationsMessage{Cmd: protocol.CmdGetPresentations}, protocol.EventGetPresentationsResult,
		func(protocol.GetPresentationsResultMessage) bool { return true })
}

func awaitBrowserTile(t *testing.T, app *testworld.Peer, workspaceID, url string) map[string]workspaceLayoutNode {
	t.Helper()
	updated := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(m protocol.WorkspaceLayoutUpdatedMessage) bool {
		if m.WorkspaceLayout.WorkspaceID != workspaceID {
			return false
		}
		tile, ok := workspaceLayoutTree(t, m.WorkspaceLayout).tiles()["tile-browser"]
		return ok && tile.TileKind == "browser" && tile.TileParams == url
	})
	return workspaceLayoutTree(t, updated.WorkspaceLayout).tiles()
}

func awaitBrowserNavigation(host *testworld.Peer, workspaceID, url string) {
	host.T.Helper()
	testworld.Await(host, protocol.EventBrowserControlRequest, func(r protocol.BrowserControlRequestMessage) bool {
		return r.Action == "navigate" && r.WorkspaceID == workspaceID && protocol.Deref(r.Text) == url
	})
}

func closeBrowserWorkspacePane(t *testing.T, app *testworld.Peer, workspaceID, paneID string) {
	t.Helper()
	closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	}, protocol.CmdWorkspaceLayoutClosePane, workspaceID)
	if !closed.Success {
		t.Fatalf("closing pane %s: %s", paneID, protocol.Deref(closed.Error))
	}
}
