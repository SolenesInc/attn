package daemon

import "testing"

// Reusing a subscriber id makes the PTY session subscriber map overwrite the previous
// callback; the old stream's eventual detach then silently starves the live stream.
func TestWSSubscriberIDIsUniquePerAttach(t *testing.T) {
	client := &wsClient{}
	seen := map[string]int{}
	const iterations = 100
	for range iterations {
		id := wsSubscriberID(client, "session-42")
		seen[id]++
	}
	if len(seen) != iterations {
		t.Fatalf("expected %d unique subscriber ids for %d attaches, got %d", iterations, iterations, len(seen))
	}
}
