package daemon

import (
	"context"
	"slices"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) conversationsSharedByLiveSessions() map[protocol.SessionID][]protocol.SessionID {
	groups := d.store.ConversationsOpenInManySessions()
	if len(groups) == 0 {
		return nil
	}
	live := d.liveSessions(context.Background())
	shared := make(map[protocol.SessionID][]protocol.SessionID)
	for _, group := range groups {
		if session := d.store.Get(group[0]); session == nil || !conversationOpensInManyProcesses(session.Agent) {
			continue
		}
		running := slices.DeleteFunc(group, func(id protocol.SessionID) bool {
			_, ok := live[id]
			return !ok
		})
		if len(running) < 2 {
			continue
		}
		for _, id := range running {
			shared[id] = slices.DeleteFunc(slices.Clone(running), func(other protocol.SessionID) bool { return other == id })
		}
	}
	return shared
}

func conversationOpensInManyProcesses(agent protocol.SessionAgent) bool {
	return agentdriver.EffectiveCapabilities(agentdriver.Get(string(agent))).ConversationOpensInManyProcesses
}

func (d *Daemon) projectSharedConversations() {
	shared := d.conversationsSharedByLiveSessions()
	d.sharedConversationsMu.Lock()
	shown := d.sharedConversationsShown
	d.sharedConversationsShown = shared
	d.sharedConversationsMu.Unlock()
	for id, others := range shown {
		if !slices.Equal(others, shared[id]) {
			d.projectSessionStateChanged(id)
		}
	}
	for id := range shared {
		if _, wasShown := shown[id]; !wasShown {
			d.projectSessionStateChanged(id)
		}
	}
}
