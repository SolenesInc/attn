package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/protocol"
)

// Mutations can block behind automationMu for tens of seconds; set_enabled/delete/apply
// abort at the daemon-side 25s deadline, strictly inside the client's 30s timeout.

func (d *Daemon) handleAutomationDefinitionsGetWS(client *wsClient, msg *protocol.AutomationDefinitionsGetMessage) {
	result := d.actionAutomationDefinitionsGet(msg)
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutomationRunsGetWS(client *wsClient, msg *protocol.AutomationRunsGetMessage) {
	result := d.actionAutomationRunsGet(msg)
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutomationSetEnabledWS(client *wsClient, msg *protocol.AutomationSetEnabledMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationSetEnabled(ctx, msg)
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleAutomationDeleteWS(client *wsClient, msg *protocol.AutomationDeleteMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationDelete(ctx, msg)
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleAutomationCleanupWS(client *wsClient, msg *protocol.AutomationCleanupMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationCleanup(ctx, msg)
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleAutomationRunWS(client *wsClient, msg *protocol.AutomationRunMessage) {
	go func() {
		result := d.actionAutomationRun(context.Background(), msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationApplyWS backs the app editor's Save. The app always sends
// expected_id/expected_revision, which enforces guards absent on the socket/CLI path.
func (d *Daemon) handleAutomationApplyWS(client *wsClient, msg *protocol.AutomationApplyMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationApply(ctx, msg)
		d.sendToClient(client, result)
	}()
}

// handleAutomationValidateWS runs on its own goroutine: validation shells out to git
// per location override, and the dispatcher calls handlers inline on the read loop.
func (d *Daemon) handleAutomationValidateWS(client *wsClient, msg *protocol.AutomationValidateMessage) {
	go func() {
		result := d.actionAutomationValidate(msg)
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleAutomationDefinitionGetWS(client *wsClient, msg *protocol.AutomationDefinitionGetMessage) {
	result := d.actionAutomationDefinitionGet(msg)
	d.sendToClient(client, result)
}
