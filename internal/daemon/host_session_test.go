package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAgentPromptWithoutAHostSettlesTheRun(t *testing.T) {
	hub := newWSHub()
	var broadcast [][]byte
	hub.wireTap = func(payload []byte) { broadcast = append(broadcast, payload) }
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.wsHub = hub
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Agent:          "nisse",
		State:          protocol.StateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	client := &wsClient{send: make(chan outboundMessage, 10)}

	d.handleAgentPrompt(client, &protocol.AgentPromptMessage{
		Cmd:     protocol.CmdAgentPrompt,
		ID:      "sess-1",
		InputID: "input-1",
		Text:    "hello",
	})

	var settled *protocol.AgentEventMessage
	for _, payload := range broadcast {
		var event protocol.AgentEventMessage
		if err := json.Unmarshal(payload, &event); err != nil || event.Event != protocol.EventAgentEvent {
			continue
		}
		if event.ID == "sess-1" && event.Kind == "run_settled" {
			settled = &event
			break
		}
	}
	if settled == nil {
		t.Fatalf("no run_settled for the undeliverable prompt; broadcast %d messages", len(broadcast))
	}
	if settled.Seq != 0 {
		t.Fatalf("seq = %d, want 0: the daemon's own envelope is not a point on the host's spine", settled.Seq)
	}
	if reason, _ := settled.Body["error"].(string); reason == "" {
		t.Fatalf("run_settled body = %v, want an error naming why the prompt went nowhere", settled.Body)
	}
}
