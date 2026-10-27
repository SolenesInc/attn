package daemon

import (
	"context"
	"slices"

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

func (d *Daemon) projectSharedConversations(subject protocol.SessionID) {
	if d.conversationShowsInManyTerminals(subject) {
		return
	}
	d.projectSessionsUpdated()
}
