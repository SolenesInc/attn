package daemon

import (
	"context"
	"fmt"
	"slices"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

// pluginLink delivers through a plugin driver's message_delivery capability, as pi does.
type pluginLink struct{ daemon *Daemon }

func (pluginLink) Voices() []harness.Voice {
	return []harness.Voice{harness.VoiceUser, harness.VoiceAttn}
}

// Deliver runs the plugin call on its own deadline and ignores ctx, so daemon
// shutdown does not cut a delivery short.
func (l pluginLink) Deliver(_ context.Context, in harness.Input) harness.Custody {
	cursor := l.daemon.store.GetAgentDriverRun(in.Session)
	ctx, cancel := context.WithTimeout(context.Background(), pluginDeliverMessageTimeout)
	defer cancel()
	var result pluginDeliverMessageResult
	params := pluginDeliverMessageParams{SessionID: in.Session, RunID: cursor.RunID, InputID: in.ID, Text: in.Text}
	if err := l.daemon.callPlugin(ctx, cursor.PluginName, "driver.deliver_message", params, &result); err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver message via plugin %q: %v", cursor.PluginName, err)}
	}
	if !result.OK {
		return harness.Custody{Reason: fmt.Sprintf("plugin %q declined message delivery for session %s", cursor.PluginName, in.Session)}
	}
	return harness.Custody{Taken: true}
}

func (d *Daemon) sessionLink(session *protocol.Session, voice harness.Voice) harness.Link {
	if !d.sessionUsesPluginMessageDelivery(session) {
		return nil
	}
	link := pluginLink{daemon: d}
	if !slices.Contains(link.Voices(), voice) {
		return nil
	}
	return link
}
