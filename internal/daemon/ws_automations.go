package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/protocol"
)

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

func (d *Daemon) handleAutomationApplyWS(client *wsClient, msg *protocol.AutomationApplyMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.wsAutomationMutationTimeoutDuration())
		defer cancel()
		result := d.actionAutomationApply(ctx, msg)
		d.sendToClient(client, result)
	}()
}

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
