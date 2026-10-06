package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func newProtocolTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 32),
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
	}
}

func expectSpawnResult(t *testing.T, client *wsClient, sessionID string, success bool) protocol.SpawnResultMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.SpawnResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventSpawnResult {
				continue
			}
			if string(result.ID) != sessionID {
				continue
			}
			if result.Success != success {
				t.Fatalf("spawn success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			return result
		case <-deadline:
			t.Fatalf("timed out waiting for spawn_result for %s", sessionID)
		}
	}
}
