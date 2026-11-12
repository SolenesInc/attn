package daemon

import (
	"context"
	"fmt"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
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

func (d *Daemon) harnessTargets(harness, cwd string) ([]protocol.ExecutionTarget, error) {
	if cwd == "" {
		return nil, fmt.Errorf("harness_targets needs a cwd")
	}
	driver := agentdriver.Get(harness)
	server := d.serverLinkFor(harness)
	if driver == nil || server == nil || !agentdriver.EffectiveCapabilities(driver).HasExecutionTarget {
		return nil, fmt.Errorf("agent %q has no execution targets", harness)
	}
	ctx, cancel := context.WithTimeout(d.life.Context(), harnessModelDiscoveryTimeout)
	defer cancel()
	return server.targets(ctx, cwd)
}
