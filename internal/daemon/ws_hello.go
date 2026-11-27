package daemon

import (
	"crypto/subtle"
	"fmt"
	"strings"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleClientHello(client *wsClient, msg *protocol.ClientHelloMessage) {
	if refusal := d.helloRefusal(client.relay, msg); refusal != nil {
		d.logf("rejecting client hello from kind=%q: client_token mismatch", msg.ClientKind)
		d.sendToClient(client, refusal)
		client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, protocol.ErrorCodeUnauthorizedClient)
		return
	}
	requiredToken := config.BrowserHostToken()
	if client.relay != nil {
		requiredToken = client.relay.BrowserHostToken
	}
	client.setBrowserHostAuthenticated(tokenMatches(requiredToken, protocol.Deref(msg.BrowserHostToken)))
	client.setIdentity(msg.ClientKind, msg.Version, msg.Capabilities)
	if version, channel := protocol.Deref(msg.AppVersion), protocol.Deref(msg.AppChannel); version != "" || channel != "" {
		d.appIdentity.Store(&appIdentity{Version: version, Channel: channel})
	}
	if client.relay == nil {
		client.setProcessID(protocol.Deref(msg.AppPid))
	}
	client.updateReadLimit()
	d.scopeClientToProfile(client, protocol.Deref(msg.ProfileID))
	d.logf(
		"client hello: kind=%q version=%q client_id=%q capabilities=%v relayed_from=%q",
		msg.ClientKind,
		msg.Version,
		strings.TrimSpace(protocol.Deref(msg.ClientID)),
		msg.Capabilities,
		client.relayedFrom(),
	)
	d.admitClient(client)
}

func (d *Daemon) admitClient(client *wsClient) {
	client.admitted.Do(func() {
		if !d.wsHub.add(client) {
			client.abortTransport()
			return
		}
		d.logf("WebSocket client connected (%d total)", d.wsHub.ClientCount())
		d.scheduleInitialState(client)
	})
}

func tokenMatches(required, provided string) bool {
	provided = strings.TrimSpace(provided)
	return required != "" && subtle.ConstantTimeCompare([]byte(required), []byte(provided)) == 1
}

// helloRefusal is the command_error that refuses msg, or nil when it proves the client token: this daemon's, or for
// an app relayed from another machine, that machine's.
func (d *Daemon) helloRefusal(relay *appRelay, msg *protocol.ClientHelloMessage) *protocol.WebSocketEvent {
	required := d.clientToken
	reason := fmt.Sprintf(
		"client_hello refused: client_token does not match this daemon's. Read it from %s (owner-only) and send it as client_token; the daemon serving instance %q minted it.",
		config.ClientTokenPath(),
		config.Instance(),
	)
	switch {
	case relay != nil:
		required = relay.ClientToken
		reason = "client_hello refused: client_token does not match the one of the daemon this app connected to, which relayed it here."
	case d.clientToken == "":
		reason = "client_hello refused: this daemon minted no client token, so it can authorize nobody. It was started without Daemon.Start."
	}
	if tokenMatches(required, protocol.Deref(msg.ClientToken)) {
		return nil
	}
	return &protocol.WebSocketEvent{
		Event:     protocol.EventCommandError,
		Cmd:       protocol.Ptr(protocol.CmdClientHello),
		Success:   protocol.Ptr(false),
		Error:     protocol.Ptr(reason),
		ErrorCode: protocol.Ptr(protocol.ErrorCodeUnauthorizedClient),
	}
}
