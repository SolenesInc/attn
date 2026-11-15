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

func TestClientHelloWithTheMintedTokenIsAdmitted(t *testing.T) {
	w := newWorld(t)
	if config.ClientToken() == "" {
		t.Fatalf("the daemon minted no client token at %s", config.ClientTokenPath())
	}

	app := w.App()

	testworld.Request(app, protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, protocol.EventSettingsUpdated,
		func(protocol.WebSocketEvent) bool { return true })
}

func TestClientHelloWithoutATokenIsRefusedAndToldWhereTheTokenLives(t *testing.T) {
	w := newWorld(t)

	impostor := w.Connect(helloWithToken(nil), nil)

	refusal := testworld.Refused(impostor)
	if got := protocol.Deref(refusal.ErrorCode); got != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("error_code = %q, want %q", got, protocol.ErrorCodeUnauthorizedClient)
	}
	if message := protocol.Deref(refusal.Error); !strings.Contains(message, config.ClientTokenPath()) {
		t.Fatalf("refusal %q does not name the token file %s", message, config.ClientTokenPath())
	}
	expectRefusalClose(t, impostor)
}

func TestClientHelloWithAnotherInstancesTokenIsRefused(t *testing.T) {
	w := newWorld(t)
	anotherInstancesToken := strings.Repeat("0f", 32)
	if anotherInstancesToken == config.ClientToken() {
		t.Fatal("the daemon minted the token chosen to stand for another instance's")
	}

	neighbour := w.Connect(helloWithToken(protocol.Ptr(anotherInstancesToken)), nil)

	if got := protocol.Deref(testworld.Refused(neighbour).ErrorCode); got != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("error_code = %q, want %q", got, protocol.ErrorCodeUnauthorizedClient)
	}
	expectRefusalClose(t, neighbour)
}

func helloWithToken(token *string) protocol.ClientHelloMessage {
	return protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "remote-web",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{},
		ClientToken:  token,
	}
}

func expectRefusalClose(t *testing.T, p *testworld.Peer) {
	t.Helper()
	status := p.Closed()
	if status.Code != websocket.StatusPolicyViolation || status.Reason != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("closed with %d %q, want %d %q", status.Code, status.Reason, websocket.StatusPolicyViolation, protocol.ErrorCodeUnauthorizedClient)
	}
	var seen []string
	for _, e := range p.Received() {
		seen = append(seen, e.Event)
	}
	if !slices.Equal(seen, []string{protocol.EventCommandError}) {
		t.Fatalf("a refused client received %v before the close, want only its refusal", seen)
	}
}

func TestOnlyAnAppHelloWithTheHostTokenAndItsOwnOriginIsTrusted(t *testing.T) {
	w := newFsWorld(t)
	external := fsDir(t, "external")
	tauriOrigin := http.Header{"Origin": {"tauri://localhost"}}
	webPage := http.Header{"Origin": {"http://localhost:5173"}}
	for _, c := range []struct {
		name      string
		kind      string
		hostToken *string
		header    http.Header
		trusted   bool
	}{
		{"the native app with the host token", protocol.ClientKindNativeApp, protocol.Ptr(config.BrowserHostToken()), nil, true},
		{"the native app without the host token", protocol.ClientKindNativeApp, nil, nil, false},
		{"the native app with a wrong host token", protocol.ClientKindNativeApp, protocol.Ptr("not-the-host-token"), nil, false},
		{"a web page claiming to be the native app", protocol.ClientKindNativeApp, protocol.Ptr(config.BrowserHostToken()), webPage, false},
		{"the native app from the Tauri origin", protocol.ClientKindNativeApp, protocol.Ptr(config.BrowserHostToken()), tauriOrigin, false},
		{"the Tauri app with the host token", protocol.ClientKindTauriApp, protocol.Ptr(config.BrowserHostToken()), tauriOrigin, true},
		{"the Tauri app without the host token", protocol.ClientKindTauriApp, nil, tauriOrigin, false},
		{"the Tauri app without its origin", protocol.ClientKindTauriApp, protocol.Ptr(config.BrowserHostToken()), nil, false},
	} {
		hello := helloWithToken(protocol.Ptr(config.ClientToken()))
		hello.ClientKind = c.kind
		hello.BrowserHostToken = c.hostToken
		p := w.Connect(hello, c.header)
		testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)

		listed := fsAskList(p, external, "")
		if listed.Success != c.trusted {
			t.Errorf("%s: listing an explicit root succeeded=%v (%s), want trusted=%v", c.name, listed.Success, protocol.Deref(listed.Error), c.trusted)
		}
	}
}

func TestANativeAppHelloWithoutTheClientTokenIsRefused(t *testing.T) {
	w := newFsWorld(t)
	hello := helloWithToken(nil)
	hello.ClientKind = protocol.ClientKindNativeApp
	hello.BrowserHostToken = protocol.Ptr(config.BrowserHostToken())

	impostor := w.Connect(hello, nil)

	if got := protocol.Deref(testworld.Refused(impostor).ErrorCode); got != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("error_code = %q, want %q", got, protocol.ErrorCodeUnauthorizedClient)
	}
	expectRefusalClose(t, impostor)
}

func TestOnlyATrustedAppMaySendFramesPastTheSocketFrameLimit(t *testing.T) {
	w := newFsWorld(t)
	oversized := map[string]any{
		"cmd": protocol.CmdGetSettings,
		"pad": strings.Repeat("x", protocol.MaxSocketFrameBytes+1<<20),
	}
	hello := helloWithToken(protocol.Ptr(config.ClientToken()))
	hello.ClientKind = protocol.ClientKindNativeApp

	untrusted := w.Connect(hello, nil)
	testworld.Await[protocol.InitialStateMessage](untrusted, protocol.EventInitialState, nil)
	untrusted.Send(oversized)
	if status := untrusted.Closed(); status.Code != websocket.StatusMessageTooBig {
		t.Fatalf("an untrusted client's oversized frame closed it with %d %q, want %d", status.Code, status.Reason, websocket.StatusMessageTooBig)
	}

	hello.BrowserHostToken = protocol.Ptr(config.BrowserHostToken())
	native := w.Connect(hello, nil)
	testworld.Await[protocol.InitialStateMessage](native, protocol.EventInitialState, nil)
	testworld.Request(native, oversized, protocol.EventSettingsUpdated,
		func(protocol.WebSocketEvent) bool { return true })
}
