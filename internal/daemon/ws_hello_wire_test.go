package daemon_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClientHelloWithTheMintedTokenIsAdmitted(t *testing.T) {
	w := newWorld(t)
	if config.ClientToken() == "" {
		t.Fatalf("the daemon minted no client token at %s", config.ClientTokenPath())
	}

	app := w.app()

	request(app, protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, protocol.EventSettingsUpdated,
		func(protocol.WebSocketEvent) bool { return true })
}

func TestClientHelloWithoutATokenIsRefusedAndToldWhereTheTokenLives(t *testing.T) {
	w := newWorld(t)

	impostor := w.connect(helloWithToken(nil), nil)
	impostor.send(protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings})

	refusal := refused(impostor)
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

	neighbour := w.connect(helloWithToken(protocol.Ptr(anotherInstancesToken)), nil)

	if got := protocol.Deref(refused(neighbour).ErrorCode); got != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("error_code = %q, want %q", got, protocol.ErrorCodeUnauthorizedClient)
	}
	expectRefusalClose(t, neighbour)
}

func TestBearerAuthorizedClientNeedsNoClientToken(t *testing.T) {
	t.Setenv("ATTN_WS_AUTH_TOKEN", "operator-bearer")
	w := newWorld(t)

	remote := w.connect(helloWithToken(nil), http.Header{"Authorization": {"Bearer operator-bearer"}})

	await[protocol.InitialStateMessage](remote, protocol.EventInitialState, nil)
	request(remote, protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, protocol.EventSettingsUpdated,
		func(protocol.WebSocketEvent) bool { return true })
}

func helloWithToken(token *string) protocol.ClientHelloMessage {
	return protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "remote-web",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:  token,
	}
}

func expectRefusalClose(t *testing.T, p *peer) {
	t.Helper()
	status := p.closed()
	if status.Code != websocket.StatusPolicyViolation || status.Reason != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("closed with %d %q, want %d %q", status.Code, status.Reason, websocket.StatusPolicyViolation, protocol.ErrorCodeUnauthorizedClient)
	}
	if seen := p.events(); !slices.Equal(seen, []string{protocol.EventCommandError}) {
		t.Fatalf("a refused client received %v before the close, want only its refusal", seen)
	}
}
