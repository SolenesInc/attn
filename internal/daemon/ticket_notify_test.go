package daemon

import (
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func delegateForNotify(t *testing.T, d *Daemon, agent string) (chiefID, agentID string, inputs func(string) []string) {
	t.Helper()
	backend := &fakeSpawnBackend{}
	var mu sync.Mutex
	rec := map[string][]string{}
	backend.onInput = func(id string, data []byte) {
		mu.Lock()
		rec[id] = append(rec[id], string(data))
		mu.Unlock()
	}
	_, chiefID, _ = setupDelegationSource(t, d, backend)
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, chiefID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	setSessionAgent(t, d, chiefID, protocol.SessionAgentClaude)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(chiefID),
		Brief:           protocol.Ptr("Migrate the store to X"),
		Agent:           protocol.Ptr(agent),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	bindLegacyTicket(t, d, result.SessionID, chiefID)
	inputs = func(id string) []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), rec[id]...)
	}
	return chiefID, result.SessionID, inputs
}

func setSessionAgent(t *testing.T, d *Daemon, sessionID string, agent protocol.SessionAgent) {
	t.Helper()
	s := d.store.Get(sessionID)
	if s == nil {
		t.Fatalf("setSessionAgent: session %s not found", sessionID)
	}
	s.Agent = agent
	d.store.Add(s)
}

func wasNudged(inputs []string) bool {
	for _, in := range inputs {
		if strings.Contains(in, agentMailboxDoorbellText) {
			return true
		}
	}
	return false
}

func commentOnTicket(t *testing.T, d *Daemon, ticketID, comment string) {
	t.Helper()
	if resp := callTicketComment(t, d, store.TicketAuthorYou, ticketID, comment); !resp.Ok {
		t.Fatalf("comment on %s: %v", ticketID, protocol.Deref(resp.Error))
	}
}
