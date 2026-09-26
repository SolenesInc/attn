package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAnEvictionFiledMidHelloIsNotLostWithTheConnection(t *testing.T) {
	d := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	defer d.Stop()

	const clientID = "app-instance-1"
	client := &wsClient{
		send:             make(chan outboundMessage, 8),
		bearerAuthorized: true,
	}

	client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, slowClientCloseReason)
	d.wsHub.rememberEviction(clientID, evictionRecord{
		at: time.Now(), reason: slowClientCloseReason, undelivered: maxSlowCount,
	})

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		Cmd: protocol.CmdClientHello, ClientKind: "daemon-test", ClientID: protocol.Ptr(clientID),
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
	})

	record, ok := d.wsHub.takeEviction(clientID)
	if !ok {
		t.Fatal("an eviction filed mid-hello was lost: the next connection will never be told why")
	}
	if record.reason != slowClientCloseReason {
		t.Errorf("re-filed reason = %q, want %q", record.reason, slowClientCloseReason)
	}
	if record.undelivered < maxSlowCount {
		t.Errorf("re-filed undelivered = %d, want at least %d", record.undelivered, maxSlowCount)
	}
}
