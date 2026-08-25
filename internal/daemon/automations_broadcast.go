package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) broadcastAutomationsChanged(definitionIDs ...string) {
	if d == nil {
		return
	}
	d.coalesceSnapshots(func() {
		for _, id := range definitionIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			d.publishFact(FactAutomationChanged, id, nil)
		}
	})
}

func (d *Daemon) projectAutomationsChanged(definitionIDs ...string) {
	if d == nil || len(definitionIDs) == 0 {
		return
	}
	msg := &protocol.AutomationsChangedMessage{
		Event:         protocol.EventAutomationsChanged,
		DefinitionIds: definitionIDs,
	}
	if d.automationsBroadcastHook != nil {
		d.automationsBroadcastHook(msg)
	}
	if d.wsHub != nil {
		d.wsHub.BroadcastValue(msg)
	}
}
