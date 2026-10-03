package daemon

import (
	"strconv"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) broadcastAutomationsChanged(definitionIDs ...int) {
	if d == nil {
		return
	}
	d.coalesceSnapshots(func() {
		for _, id := range definitionIDs {
			if id == 0 {
				continue
			}
			d.publishFact(FactAutomationChanged, strconv.Itoa(id), nil)
		}
	})
}

func (d *Daemon) projectAutomationsChanged(definitionIDs ...int) {
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
