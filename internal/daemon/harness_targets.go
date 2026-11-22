package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleHarnessTargets(client *wsClient, msg *protocol.HarnessTargetsMessage) {
	result := protocol.HarnessTargetsResultMessage{Event: protocol.EventHarnessTargetsResult, RequestID: msg.RequestID, Targets: []protocol.ExecutionTarget{}}
	targets, err := d.harnessTargets(strings.TrimSpace(msg.Harness), strings.TrimSpace(msg.Cwd))
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success, result.Targets = true, targets
	}
	d.sendToClient(client, result)
}

func (d *Daemon) harnessTargets(id, cwd string) ([]protocol.ExecutionTarget, error) {
	if cwd == "" {
		return nil, fmt.Errorf("harness_targets needs a cwd")
	}
	targets, ok := d.targetsOf(id)
	if !ok {
		return nil, fmt.Errorf("agent %q has no execution targets", id)
	}
	ctx, cancel := context.WithTimeout(d.life.Context(), harnessModelDiscoveryTimeout)
	defer cancel()
	return targets.Targets(ctx, cwd)
}

func (d *Daemon) targetsOf(id string) (harness.Targets, bool) {
	return harness.As[harness.Targets](d.harnessOf(id))
}
