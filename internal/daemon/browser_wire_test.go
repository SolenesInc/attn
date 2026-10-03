package daemon_test

import (
	"net/http"
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
		if _, err := cli.BrowserCommand(action, "", "#query", "https://example.com", ""); err == nil || !strings.Contains(err.Error(), "no browser tile is open") {
			t.Errorf("browser %s = %v, want it accepted up to finding its browser tile", action, err)
		}
	}
}

func TestAttnBrowserOpenDocksBesideTheFocusedAgent(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	host := browserHostPeer(w, "tauri://localhost", browserHostToken)
	shop, shopDesktop, shopPane := w.RequestSpawn(app, shellHarness, w.Path("shop"))
	focusAgent(t, w, app, shop.ID)

	if err := cli.OpenBrowser("  http://localhost:3000/path  ", ""); err != nil {
		t.Fatalf("attn browser open: %v", err)
	}
	awaitBrowserTile(t, app, shopDesktop, "http://localhost:3000/path")

	if err := cli.OpenBrowser("http://localhost:3000/path", ""); err != nil {
		t.Fatalf("attn browser open again: %v", err)
	}
	awaitBrowserNavigation(host, shopDesktop, "http://localhost:3000/path")
	if leaves := desktopTree(t, desktopOfDelegate(t, w, shopDesktop)).leafIDs(); len(leaves) != 2 || leaves[0] != shopPane {
		t.Errorf("after opening the same URL the desktop holds %v, want the pane and one browser tile", leaves)
	}

	closeFromApp(app, shop.ID)
	if err := cli.OpenBrowser("https://example.com/retargeted", ""); err != nil {
		t.Fatalf("attn browser open on the desktop without agents: %v", err)
	}
	awaitBrowserTile(t, app, shopDesktop, "https://example.com/retargeted")
	awaitBrowserNavigation(host, shopDesktop, "https://example.com/retargeted")
}

func TestBrowserControlIsBrokeredToTheHostThatWasAsked(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop, shopDesktop, _ := w.RequestSpawn(app, shellHarness, w.Path("shop"))
	docs, _, _ := w.RequestSpawn(app, shellHarness, w.Path("docs"))
	for _, session := range []string{shop.ID, docs.ID} {
		if err := cli.OpenBrowser("http://localhost:3000", session); err != nil {
			t.Fatalf("open a browser for %s: %v", session, err)
		}
	}
	focusAgent(t, w, app, shop.ID)
	spoof := browserHostPeer(w, "tauri://localhost", browserHostToken)
	host := browserHostPeer(w, "http://tauri.localhost", browserHostToken)

	answered := make(chan browserControlAnswer, 1)
	go func() {
		data, err := w.Client().BrowserCommand("type", "", "#query", "browser text", "")
		answered <- browserControlAnswer{data, err}
	}()
	request := testworld.Await(host, protocol.EventBrowserControlRequest, func(r protocol.BrowserControlRequestMessage) bool { return r.Action == "type" })
	if request.DesktopID != shopDesktop || protocol.Deref(request.Selector) != "#query" || protocol.Deref(request.Text) != "browser text" {
		t.Errorf("the host was asked %+v, want to type into #query on %s", request, shopDesktop)
	}
	spoof.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: request.RequestID, Success: true, Data: protocol.Ptr("spoofed")})
	browserPeerCaughtUp(spoof)
	large := strings.Repeat("A", 2<<20)
	host.Send(protocol.BrowserControlResultMessage{Cmd: protocol.CmdBrowserControlResult, RequestID: request.RequestID, Success: true, Data: protocol.Ptr(large)})
	if got := <-answered; got.err != nil || got.data != large {
		t.Errorf("the CLI got %d bytes (%v), want the host's %d-byte result and not the spoof's", len(got.data), got.err, len(large))
	}

	ordinary := w.App()
	ordinary.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: shop.ID, Data: strings.Repeat("x", 1<<20)})
	if status := ordinary.Closed(); status.Code != websocket.StatusMessageTooBig {
		t.Errorf("an app peer sending a message past the command-sized limit was closed with %d, want %d", status.Code, websocket.StatusMessageTooBig)
	}
	exitShells(app, shop.ID, docs.ID)
}

func TestOnlyTheAppsOriginWithTheHostTokenBecomesTheBrowserHost(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shop, _, _ := w.RequestSpawn(app, shellHarness, w.Path("shop"))
	if err := cli.OpenBrowser("http://localhost:3000", shop.ID); err != nil {
		t.Fatalf("open a browser: %v", err)
	}
	focusAgent(t, w, app, shop.ID)

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
	exitShells(app, shop.ID)
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
		Capabilities:     []string{protocol.CapabilityBrowserHost},
		ClientToken:      protocol.Ptr(config.ClientToken()),
		BrowserHostToken: protocol.Ptr(token),
	}, header)
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}

func browserPeerCaughtUp(p *testworld.Peer) {
	p.T.Helper()
	testworld.Request(p, protocol.GetPresentationsMessage{Cmd: protocol.CmdGetPresentations}, protocol.EventGetPresentationsResult,
		func(protocol.GetPresentationsResultMessage) bool { return true })
}

func awaitBrowserTile(t *testing.T, app *testworld.Peer, desktopID, url string) map[string]layoutNode {
	t.Helper()
	desktop := awaitDesktop(app, desktopID, func(d protocol.Desktop) bool {
		for _, tile := range desktopTree(t, d).tiles() {
			if tile.TileKind == "browser" && tile.TileParams == url {
				return true
			}
		}
		return false
	})
	return desktopTree(t, desktop).tiles()
}

func awaitBrowserNavigation(host *testworld.Peer, desktopID, url string) {
	host.T.Helper()
	testworld.Await(host, protocol.EventBrowserControlRequest, func(r protocol.BrowserControlRequestMessage) bool {
		return r.Action == "navigate" && r.DesktopID == desktopID && protocol.Deref(r.Text) == url
	})
}
