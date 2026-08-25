package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
)

// Auto mode's app-only half: an agent reaches the unix socket, a human reaches the app, and
// only a human may change the policy a session runs under. Do not add a CLI equivalent.

func (d *Daemon) handleAutoModeGet(client *wsClient, msg *protocol.AutoModeGetMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeGet, "automode_get is missing a request id")
		return
	}
	result := protocol.AutoModeStateResultMessage{
		Event:     protocol.EventAutoModeStateResult,
		RequestID: requestID,
	}
	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	proposals, err := d.store.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.reconcileAutoModeDenialLedger()
	denials, err := d.store.ListAutoModeDenials(automodeDenialsDefaultLimit)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Config = autoModeConfigInfo(cfg)
	result.Proposals = autoModeProposalInfos(proposals)
	result.Denials = autoModeDenialInfos(denials)
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutoModePromote(client *wsClient, msg *protocol.AutoModePromoteMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModePromote, "automode_promote is missing a request id")
		return
	}
	result := protocol.AutoModePromoteResultMessage{
		Event:     protocol.EventAutoModePromoteResult,
		RequestID: requestID,
	}
	proposal, cfg, err := d.store.PromoteAutoModeProposal(int64(msg.ID), time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.logf("automode: promoted proposal %d (%s %s)", proposal.ID, proposal.Kind, proposal.Value)
	info := autoModeProposalInfo(proposal)
	config := autoModeConfigInfo(cfg)
	result.Proposal = &info
	result.Config = &config
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutoModeDiscard(client *wsClient, msg *protocol.AutoModeDiscardMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeDiscard, "automode_discard is missing a request id")
		return
	}
	result := protocol.AutoModeDiscardResultMessage{
		Event:     protocol.EventAutoModeDiscardResult,
		RequestID: requestID,
	}
	proposal, err := d.store.DiscardAutoModeProposal(int64(msg.ID), time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	info := autoModeProposalInfo(proposal)
	result.Proposal = &info
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutoModePatternAdd(client *wsClient, msg *protocol.AutoModePatternAddMessage) {
	d.editAutoModePattern(client, protocol.CmdAutoModePatternAdd, msg.RequestID, msg.List, msg.Pattern,
		d.store.AddAutoModePattern)
}

func (d *Daemon) handleAutoModePatternRemove(client *wsClient, msg *protocol.AutoModePatternRemoveMessage) {
	d.editAutoModePattern(client, protocol.CmdAutoModePatternRemove, msg.RequestID, msg.List, msg.Pattern,
		d.store.RemoveAutoModePattern)
}

func (d *Daemon) editAutoModePattern(
	client *wsClient,
	cmd, requestID, list, pattern string,
	edit func(list, pattern string, now time.Time) (automode.Config, error),
) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		d.sendCommandError(client, cmd, cmd+" is missing a request id")
		return
	}
	result := protocol.AutoModePatternResultMessage{
		Event:     protocol.EventAutoModePatternResult,
		RequestID: requestID,
	}
	cfg, err := edit(list, pattern, time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.logf("automode: %s %s %q", cmd, list, pattern)
	info := autoModeConfigInfo(cfg)
	result.Config = &info
	result.Success = true
	d.sendToClient(client, result)
}
