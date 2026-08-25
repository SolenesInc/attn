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
	if !d.authorizeClientHello(client, msg) {
		return
	}
	requiredToken := config.BrowserHostToken()
	providedToken := strings.TrimSpace(protocol.Deref(msg.BrowserHostToken))
	client.setBrowserHostAuthenticated(
		requiredToken != "" &&
			subtle.ConstantTimeCompare([]byte(requiredToken), []byte(providedToken)) == 1,
	)
	client.setIdentity(msg.ClientKind, msg.Version, msg.Capabilities)
	clientID := strings.TrimSpace(protocol.Deref(msg.ClientID))
	client.setClientID(clientID)
	client.updateReadLimit()
	d.logf(
		"client hello: kind=%q version=%q client_id=%q capabilities=%v",
		msg.ClientKind,
		msg.Version,
		clientID,
		msg.Capabilities,
	)
	d.admitClient(client)
	if record, ok := d.wsHub.takeEviction(clientID); ok {
		if !d.sendEvictionNotice(client, record) {
			// Consuming the record here would lose the only copy, so it goes back on file
			// for the next hello; a delivered notice is never re-filed.
			d.wsHub.rememberEviction(clientID, record)
		}
	}
}

// admitClient lets an authorized connection into the hub. The hub is the only fan-out, so
// an unauthorized connection sees no broadcast at all. A double hello joins once.
func (d *Daemon) admitClient(client *wsClient) {
	client.admitted.Do(func() {
		d.wsHub.add(client)
		d.logf("WebSocket client connected (%d total)", d.wsHub.ClientCount())
		d.scheduleInitialState(client)
	})
}

func (d *Daemon) authorizeClientHello(client *wsClient, msg *protocol.ClientHelloMessage) bool {
	if client.bearerAuthorized {
		return true
	}
	// The d.clientToken != "" half matters: a daemon holding no token refuses
	// everyone rather than matching a client that also sent nothing.
	provided := strings.TrimSpace(protocol.Deref(msg.ClientToken))
	if d.clientToken != "" && subtle.ConstantTimeCompare([]byte(d.clientToken), []byte(provided)) == 1 {
		return true
	}
	reason := fmt.Sprintf(
		"client_hello refused: client_token does not match this daemon's. Read it from %s (owner-only) and send it as client_token; the daemon serving profile %q minted it.",
		config.ClientTokenPath(),
		config.Profile(),
	)
	if d.clientToken == "" {
		reason = "client_hello refused: this daemon minted no client token, so it can authorize nobody. It was started without Daemon.Start."
	}
	d.logf("rejecting client hello from kind=%q: client_token mismatch", msg.ClientKind)
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:     protocol.EventCommandError,
		Cmd:       protocol.Ptr(protocol.CmdClientHello),
		Success:   protocol.Ptr(false),
		Error:     protocol.Ptr(reason),
		ErrorCode: protocol.Ptr(protocol.ErrorCodeUnauthorizedClient),
	})
	// Close through the send channel, not the connection: the write pump drains the
	// queue first, which is what makes the refusal arrive rather than race the close.
	client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, protocol.ErrorCodeUnauthorizedClient)
	return false
}
