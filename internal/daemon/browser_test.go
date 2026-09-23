package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"os"
)

func TestValidateBrowserURL(t *testing.T) {
	if got, err := validateBrowserURL(" http://localhost:3000/path "); err != nil || got != "http://localhost:3000/path" {
		t.Fatalf("validateBrowserURL() = (%q, %v)", got, err)
	}
	for _, raw := range []string{"", "file:///tmp/index.html", "javascript:alert(1)", "https://"} {
		if _, err := validateBrowserURL(raw); err == nil {
			t.Fatalf("validateBrowserURL(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestTrustedTauriOrigin(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	for _, origin := range []string{"tauri://localhost", "http://tauri.localhost"} {
		if !isTrustedTauriOrigin(origin) {
			t.Fatalf("isTrustedTauriOrigin(%q) = false", origin)
		}
	}
	for _, origin := range []string{"", "http://localhost:1420", "http://localhost:3000", "http://127.0.0.1:5173", "https://example.com"} {
		if isTrustedTauriOrigin(origin) {
			t.Fatalf("isTrustedTauriOrigin(%q) = true", origin)
		}
	}

	t.Setenv("ATTN_INSTANCE", "dev")
	if !isTrustedTauriOrigin("http://localhost:1420") {
		t.Fatal("documented Tauri dev origin is not trusted in the dev instance")
	}
	if isTrustedTauriOrigin("http://localhost:3000") {
		t.Fatal("unrelated localhost origin is trusted in the dev instance")
	}
}

func TestBrowserHostRequiresMatchingToken(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", "expected-secret")
	client := newWorkspaceProtocolTestClient()
	client.trustedTauriOrigin = true
	d := newHelloTestDaemon(t, "client-token")

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:       "tauri-app",
		Version:          "test",
		Capabilities:     []string{protocol.CapabilityBrowserHost},
		ClientToken:      protocol.Ptr("client-token"),
		BrowserHostToken: protocol.Ptr("wrong-secret"),
	})
	if client.IsBrowserHost() {
		t.Fatal("browser host authenticated with the wrong token")
	}

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		ClientKind:       "tauri-app",
		Version:          "test",
		Capabilities:     []string{protocol.CapabilityBrowserHost},
		ClientToken:      protocol.Ptr("client-token"),
		BrowserHostToken: protocol.Ptr("expected-secret"),
	})
	if !client.IsBrowserHost() {
		t.Fatal("browser host rejected the matching token")
	}
	if got := websocketReadLimit(client); got != maxBrowserHostWebSocketReadBytes {
		t.Fatalf("browser host read limit = %d, want %d", got, maxBrowserHostWebSocketReadBytes)
	}
}

func TestOrdinaryClientsKeepCommandSizedWebSocketLimit(t *testing.T) {
	client := newWorkspaceProtocolTestClient()
	client.trustedTauriOrigin = true
	client.browserHostAuthenticated = true
	client.setIdentity("tauri-app", "test", []string{protocol.CapabilityWorkspaceSessions})

	if got := websocketReadLimit(client); got != defaultWebSocketReadBytes {
		t.Fatalf("ordinary client read limit = %d, want %d", got, defaultWebSocketReadBytes)
	}
}

func TestBrowserControlTimeout(t *testing.T) {
	if got, err := browserControlTimeout(nil); err != nil || got != browserControlDefaultTimeout {
		t.Fatalf("browserControlTimeout(nil) = (%v, %v)", got, err)
	}
	if got, err := browserControlTimeout(map[string]any{"timeout": float64(30_000)}); err != nil || got != 35*time.Second {
		t.Fatalf("browserControlTimeout(30s) = (%v, %v)", got, err)
	}
	if _, err := browserControlTimeout(map[string]any{"timeout": float64(120_001)}); err == nil {
		t.Fatal("browserControlTimeout accepted an excessive timeout")
	}
}

func TestNormalizeBrowserAction(t *testing.T) {
	for _, action := range []string{"snapshot", "click", "type", "reload", "navigate", "screenshot", "find_element", "perform_actions", "get_all_cookies", "print_page", "wait_for"} {
		if got, err := normalizeBrowserAction(action); err != nil || got != action {
			t.Fatalf("normalizeBrowserAction(%q) = (%q, %v)", action, got, err)
		}
	}
	if _, err := normalizeBrowserAction("submit"); err == nil {
		t.Fatal("normalizeBrowserAction(submit) unexpectedly succeeded")
	}
}

func TestBrowserControlRejectsNonObjectParams(t *testing.T) {
	d, _ := setupAgentDesktop(t)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleBrowserControl(serverConn, &protocol.BrowserControlMessage{
		Cmd:    protocol.CmdBrowserControl,
		Action: "find_element",
		Params: protocol.Ptr("null"),
	})

	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "JSON object") {
		t.Fatalf("response = %+v", resp)
	}
}

func TestOpenBrowserRetargetsExistingTileAtSameURL(t *testing.T) {
	base := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		d, desktop := setupAgentDesktopOn(t, base)
		stopDaemonBackground(t, d)

		firstClient, firstServer := net.Pipe()
		go d.handleOpenBrowser(firstServer, &protocol.OpenBrowserMessage{
			Cmd: protocol.CmdOpenBrowser,
			URL: "http://localhost:3000",
		})
		var firstResp protocol.Response
		if err := json.NewDecoder(firstClient).Decode(&firstResp); err != nil || !firstResp.Ok {
			t.Fatalf("first open browser response = (%+v, %v)", firstResp, err)
		}
		_ = firstClient.Close()

		host := newWorkspaceProtocolTestClient()
		host.trustedTauriOrigin = true
		host.browserHostAuthenticated = true
		host.connectedAt = time.Now()
		host.setIdentity("tauri-app", "test", []string{
			protocol.CapabilityWorkspaceSessions,
			protocol.CapabilityBrowserHost,
		})
		d.wsHub.mu.Lock()
		d.wsHub.clients[host] = true
		d.wsHub.mu.Unlock()

		secondClient, secondServer := net.Pipe()
		defer secondClient.Close()
		go d.handleOpenBrowser(secondServer, &protocol.OpenBrowserMessage{
			Cmd: protocol.CmdOpenBrowser,
			URL: "http://localhost:3000",
		})

		request := requireBrowserControlRequest(t, host)
		if request.Event != protocol.EventBrowserControlRequest ||
			protocol.Deref(request.DesktopID) != desktop.ID ||
			request.Action != "navigate" ||
			protocol.Deref(request.Text) != "http://localhost:3000" {
			t.Fatalf("request = %+v", request)
		}

		_ = secondClient.SetReadDeadline(time.Now().Add(2 * time.Second))
		var secondResp protocol.Response
		if err := json.NewDecoder(secondClient).Decode(&secondResp); err != nil || !secondResp.Ok {
			t.Fatalf("second open browser response = (%+v, %v)", secondResp, err)
		}
	})
}

func TestOpenBrowserDocksBesideTheAgentThatOpensIt(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	_, second, err := d.store.CreateDesktop(desktop.ProfileID, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	injectTestSession(t, d, protocol.Session{ID: "session-2", Label: "other", Directory: t.TempDir()})
	if _, err := d.store.RemoveSessionPlacement("session-2"); err != nil {
		t.Fatal(err)
	}
	placeTestSession(t, d, "session-2", second.ID)
	focusTestAgent(t, d, "session-1")

	if resp := openBrowserFor(t, d, "", "http://localhost:3000"); !resp.Ok {
		t.Fatalf("open_browser for the current agent: %v", protocol.Deref(resp.Error))
	}
	if tile := desktopTile(t, d, desktop.ID, browserTileID); tile.TileParams != "http://localhost:3000" {
		t.Fatalf("browser on the current agent's desktop = %+v", tile)
	}

	if resp := openBrowserFor(t, d, "session-2", "http://localhost:4000"); !resp.Ok {
		t.Fatalf("open_browser for session-2: %v", protocol.Deref(resp.Error))
	}
	if tile := desktopTile(t, d, second.ID, browserTileID); tile.TileParams != "http://localhost:4000" {
		t.Fatalf("browser on session-2's desktop = %+v", tile)
	}
	if tile := desktopTile(t, d, desktop.ID, browserTileID); tile.TileParams != "http://localhost:3000" {
		t.Fatalf("the current agent's browser moved to %q", tile.TileParams)
	}
}

func TestOpenBrowserRetargetsTheBrowserOnATileOnlyDesktop(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	if resp := openBrowserFor(t, d, "", "http://localhost:3000"); !resp.Ok {
		t.Fatalf("first open: %v", protocol.Deref(resp.Error))
	}
	if _, err := d.store.RemoveSessionPlacement("session-1"); err != nil {
		t.Fatal(err)
	}
	if resp := openBrowserFor(t, d, "", "https://example.com/retargeted"); !resp.Ok {
		t.Fatalf("second open: %v", protocol.Deref(resp.Error))
	}
	if tile := desktopTile(t, d, desktop.ID, browserTileID); tile.TileParams != "https://example.com/retargeted" {
		t.Fatalf("retargeted browser = %+v", tile)
	}
}

func TestOpenBrowserDocksBesideTheTileOfAPanelessDesktop(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.openMarkdownTile(notes, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.RemoveSessionPlacement("session-1"); err != nil {
		t.Fatal(err)
	}
	if resp := openBrowserFor(t, d, "", "https://example.com"); !resp.Ok {
		t.Fatalf("open_browser: %v", protocol.Deref(resp.Error))
	}
	updated, err := d.store.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !browserTileInWorkspace(updated.Tree) || !layouttree.HasTile(updated.Tree, markdownTileIDForPath(notes)) {
		t.Fatalf("desktop tree = %+v, want the browser beside the notes tile", updated.Tree)
	}
}

func TestBrowserTargetFromRemoteWorkspace(t *testing.T) {
	target, err := browserTargetFromRemoteWorkspace(&protocol.Workspace{
		ID: "remote-workspace",
		Layout: &protocol.WorkspaceLayout{
			WorkspaceID:  "remote-workspace",
			ActivePaneID: "",
			LayoutJson:   `{"type":"tile","tile_id":"tile-browser","tile_kind":"browser","tile_params":"https://example.com"}`,
		},
	}, "endpoint-1")
	if err != nil {
		t.Fatalf("browserTargetFromRemoteWorkspace() error = %v", err)
	}
	if target.workspaceID != "remote-workspace" || target.remoteEndpointID != "endpoint-1" {
		t.Fatalf("target = %+v", target)
	}
	if target.anchorLeafID != browserTileID || !browserTileInWorkspace(target.layout) {
		t.Fatalf("remote target did not preserve the browser tile: %+v", target)
	}
}

func TestBrowserControlTargetUsesExplicitWorkspace(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	target, err := d.browserControlTarget(&protocol.BrowserControlMessage{
		WorkspaceID: protocol.Ptr(workspaceID),
	})
	if err != nil {
		t.Fatalf("browserControlTarget() error = %v", err)
	}
	if target.workspaceID != workspaceID {
		t.Fatalf("browserControlTarget() workspace = %q, want %q", target.workspaceID, workspaceID)
	}
}

func TestBrowserControlBrokersToCapableClient(t *testing.T) {
	base := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		d, _ := setupAgentDesktopOn(t, base)
		stopDaemonBackground(t, d)
		largeResult := strings.Repeat("A", 64*1024)

		openClient, openServer := net.Pipe()
		go d.handleOpenBrowser(openServer, &protocol.OpenBrowserMessage{
			Cmd: protocol.CmdOpenBrowser,
			URL: "http://localhost:3000",
		})
		var openResp protocol.Response
		if err := json.NewDecoder(openClient).Decode(&openResp); err != nil || !openResp.Ok {
			t.Fatalf("open browser response = (%+v, %v)", openResp, err)
		}
		_ = openClient.Close()

		host := newWorkspaceProtocolTestClient()
		host.trustedTauriOrigin = true
		host.browserHostAuthenticated = true
		host.connectedAt = time.Now()
		host.setIdentity("tauri-app", "test", []string{
			protocol.CapabilityWorkspaceSessions,
			protocol.CapabilityBrowserHost,
		})
		d.wsHub.mu.Lock()
		d.wsHub.clients[host] = true
		d.wsHub.mu.Unlock()

		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		go d.handleBrowserControl(serverConn, &protocol.BrowserControlMessage{
			Cmd:      protocol.CmdBrowserControl,
			Action:   "type",
			Selector: protocol.Ptr("#query"),
			Text:     protocol.Ptr("browser text"),
		})

		request := requireBrowserControlRequest(t, host)
		if request.Event != protocol.EventBrowserControlRequest ||
			request.Action != "type" ||
			protocol.Deref(request.Selector) != "#query" ||
			protocol.Deref(request.Text) != "browser text" {
			t.Fatalf("request = %+v", request)
		}
		d.handleBrowserControlResult(host, &protocol.BrowserControlResultMessage{
			Cmd:       protocol.CmdBrowserControlResult,
			RequestID: request.RequestID,
			Success:   true,
			Data:      protocol.Ptr(largeResult),
		})

		_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var resp protocol.Response
		if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if !resp.Ok || protocol.Deref(resp.Data) != largeResult {
			t.Fatalf("response = %+v", resp)
		}
	})
}

func TestRemoteBrowserControlReturnsResultToHubClient(t *testing.T) {
	base := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		d, _, workspaceID := setupMarkdownWorkspaceOn(t, base)
		stopDaemonBackground(t, d)
		if err := d.dockTile(workspaceID, "pane-1", browserTileID, string(layouttree.TileKindBrowser), "http://localhost:3000", "", protocol.LayoutDockEdgeRight, nil); err != nil {
			t.Fatal(err)
		}

		host := newWorkspaceProtocolTestClient()
		host.trustedTauriOrigin = true
		host.browserHostAuthenticated = true
		host.connectedAt = time.Now()
		host.setIdentity("tauri-app", "test", []string{
			protocol.CapabilityWorkspaceSessions,
			protocol.CapabilityBrowserHost,
		})
		d.wsHub.mu.Lock()
		d.wsHub.clients[host] = true
		d.wsHub.mu.Unlock()

		hubClient := newWorkspaceProtocolTestClient()
		go d.handleRemoteBrowserControl(hubClient, &protocol.BrowserControlMessage{
			Cmd:         protocol.CmdBrowserControl,
			Action:      "get_title",
			RequestID:   protocol.Ptr("remote-request-1"),
			WorkspaceID: protocol.Ptr(workspaceID),
		})

		request := requireBrowserControlRequest(t, host)
		d.handleBrowserControlResult(host, &protocol.BrowserControlResultMessage{
			Cmd:       protocol.CmdBrowserControlResult,
			RequestID: request.RequestID,
			Success:   true,
			Data:      protocol.Ptr(`"Remote title"`),
		})

		hubOutbound := requireOutbound(t, hubClient, "no browser control response reached the hub client")
		var response protocol.BrowserControlResponseMessage
		if err := json.Unmarshal(hubOutbound.payload, &response); err != nil {
			t.Fatal(err)
		}
		if response.Event != protocol.EventBrowserControlResponse ||
			response.RequestID != "remote-request-1" ||
			!response.Success ||
			protocol.Deref(response.Data) != `"Remote title"` {
			t.Fatalf("response = %+v", response)
		}
	})
}

func TestBrowserControlIgnoresResultFromDifferentHost(t *testing.T) {
	d, _ := setupAgentDesktop(t)
	openClient, openServer := net.Pipe()
	go d.handleOpenBrowser(openServer, &protocol.OpenBrowserMessage{
		Cmd: protocol.CmdOpenBrowser,
		URL: "http://localhost:3000",
	})
	var openResp protocol.Response
	if err := json.NewDecoder(openClient).Decode(&openResp); err != nil || !openResp.Ok {
		t.Fatalf("open browser response = (%+v, %v)", openResp, err)
	}
	_ = openClient.Close()

	host := newWorkspaceProtocolTestClient()
	host.trustedTauriOrigin = true
	host.browserHostAuthenticated = true
	host.connectedAt = time.Now()
	host.setIdentity("tauri-app", "test", []string{
		protocol.CapabilityWorkspaceSessions,
		protocol.CapabilityBrowserHost,
	})
	spoof := newWorkspaceProtocolTestClient()
	spoof.trustedTauriOrigin = true
	spoof.browserHostAuthenticated = true
	spoof.setIdentity("tauri-app", "test", []string{
		protocol.CapabilityWorkspaceSessions,
		protocol.CapabilityBrowserHost,
	})
	d.wsHub.mu.Lock()
	d.wsHub.clients[host] = true
	d.wsHub.clients[spoof] = true
	d.wsHub.mu.Unlock()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleBrowserControl(serverConn, &protocol.BrowserControlMessage{
		Cmd:    protocol.CmdBrowserControl,
		Action: "snapshot",
	})

	outbound := <-host.send
	var request protocol.BrowserControlRequestMessage
	if err := json.Unmarshal(outbound.payload, &request); err != nil {
		t.Fatal(err)
	}
	d.handleBrowserControlResult(spoof, &protocol.BrowserControlResultMessage{
		Cmd:       protocol.CmdBrowserControlResult,
		RequestID: request.RequestID,
		Success:   true,
		Data:      protocol.Ptr("spoofed"),
	})
	d.handleBrowserControlResult(host, &protocol.BrowserControlResultMessage{
		Cmd:       protocol.CmdBrowserControlResult,
		RequestID: request.RequestID,
		Success:   true,
		Data:      protocol.Ptr("real"),
	})

	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if got := protocol.Deref(resp.Data); got != "real" {
		t.Fatalf("response data = %q, want real", got)
	}
}
