package daemon

import (
	"testing"
)

func drainClientPayloads(t *testing.T, client *wsClient) [][]byte {
	t.Helper()
	var payloads [][]byte
	for {
		select {
		case msg, open := <-client.send:
			if !open {
				return payloads
			}
			payloads = append(payloads, msg.payload)
		default:
			return payloads
		}
	}
}

func spawnCount(backend *fakeSpawnBackend) int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.spawnOpts)
}
