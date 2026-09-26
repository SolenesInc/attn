package daemon

import (
	"encoding/json"
	"sync"
)

type WireTrace struct {
	mu       sync.Mutex
	payloads [][]byte
}

func (t *WireTrace) record(payload []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.payloads = append(t.payloads, append([]byte(nil), payload...))
}

func (t *WireTrace) Payloads() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.payloads))
	for i, p := range t.payloads {
		out[i] = append([]byte(nil), p...)
	}
	return out
}

func (t *WireTrace) EventNames() []string {
	payloads := t.Payloads()
	names := make([]string, 0, len(payloads))
	for _, p := range payloads {
		var envelope struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(p, &envelope); err != nil || envelope.Event == "" {
			names = append(names, "?")
			continue
		}
		names = append(names, envelope.Event)
	}
	return names
}

func (t *WireTrace) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.payloads = nil
}

func (t *WireTrace) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.payloads)
}
