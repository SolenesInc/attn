package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleSetSessionContextWindowCap(client *wsClient, msg *protocol.SetSessionContextWindowCapMessage) {
	err := d.setSessionContextWindowCap(msg.SessionID, msg.Cap)
	result := protocol.SessionContextWindowCapResultMessage{
		Event:     protocol.EventSessionContextWindowCapResult,
		SessionID: msg.SessionID,
		Cap:       msg.Cap,
		Success:   err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) setSessionContextWindowCap(sessionID string, cap int) error {
	if d == nil || d.store == nil {
		return fmt.Errorf("store unavailable")
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return fmt.Errorf("missing session_id")
	}
	session := d.store.Get(id)
	if session == nil {
		return fmt.Errorf("session not found")
	}
	if cap != 0 {
		if cap < contextWindowCapMin || cap > contextWindowCapMax {
			return fmt.Errorf("context window cap must be 0 (no cap) or between %d and %d tokens; got %d", contextWindowCapMin, contextWindowCapMax, cap)
		}
	}
	switch normalizeSpawnAgent(string(session.Agent)) {
	case string(protocol.SessionAgentClaude), string(protocol.SessionAgentCodex):
	default:
		return fmt.Errorf("agent %q takes no context-window cap; only claude and codex launches carry one", session.Agent)
	}
	if protocol.Deref(session.ContextWindowCap) == cap {
		return nil
	}
	if !d.store.SetSessionContextWindowCap(id, cap) {
		return fmt.Errorf("persist context-window cap failed")
	}
	d.publishFact(FactSessionCapChanged, id, nil)
	if d.sessionHasLiveWorker(id) {
		go d.reloadSessionAgent(id)
	}
	return nil
}
