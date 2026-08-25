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
	d.reconcileAutoModeDenialLedger()
	snapshot, err := d.autoModeSnapshot()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Config = snapshot.Config
	result.Proposals = snapshot.Proposals
	result.Denials = snapshot.Denials
	result.EnvironmentSlots = snapshot.EnvironmentSlots
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) autoModeSnapshot() (protocol.AutoModeStateChangedMessage, error) {
	snapshot := protocol.AutoModeStateChangedMessage{Event: protocol.EventAutoModeStateChanged}
	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		return snapshot, err
	}
	proposals, err := d.store.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		return snapshot, err
	}
	denials, err := d.store.ListAutoModeDenials(automodeDenialsDefaultLimit)
	if err != nil {
		return snapshot, err
	}
	snapshot.Config = autoModeConfigInfo(cfg)
	snapshot.Proposals = autoModeProposalInfos(proposals)
	snapshot.Denials = autoModeDenialInfos(denials)
	snapshot.EnvironmentSlots = autoModeEnvironmentSlots()
	return snapshot, nil
}

func (d *Daemon) projectAutoModeStateChanged() {
	d.projectSnapshot(snapshotAutoMode, func() {
		if d.store == nil {
			return
		}
		snapshot, err := d.autoModeSnapshot()
		if err != nil {
			d.logf("automode: could not push the config change: %v", err)
			return
		}
		d.broadcastMessage(snapshot)
	})
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
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
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
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutoModeEnvSlotWS(client *wsClient, msg *protocol.AutoModeEnvSlotMessage) {
	requestID := strings.TrimSpace(protocol.Deref(msg.RequestID))
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeEnvSlot, "automode_env_slot is missing a request id")
		return
	}
	result := protocol.AutoModeEnvSetResultMessage{
		Event:     protocol.EventAutoModeEnvSetResult,
		RequestID: requestID,
	}
	cfg, err := d.store.SetAutoModeEnvironmentSlot(msg.Slot, msg.Values, time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	filled, total := cfg.Environment.Filled()
	d.logf("automode: environment slot %s set to %d entries (%d of %d slots filled)",
		msg.Slot, len(msg.Values), filled, total)
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	info := autoModeConfigInfo(cfg)
	result.Config = &info
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAutoModeEnvNotesWS(client *wsClient, msg *protocol.AutoModeEnvNotesMessage) {
	requestID := strings.TrimSpace(protocol.Deref(msg.RequestID))
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeEnvNotes, "automode_env_notes is missing a request id")
		return
	}
	result := protocol.AutoModeEnvSetResultMessage{
		Event:     protocol.EventAutoModeEnvSetResult,
		RequestID: requestID,
	}
	cfg, err := d.store.SetAutoModeEnvironmentNotes(cleanEnvironmentLines(msg.Notes), time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.logf("automode: environment notes set to %d lines", len(cfg.Environment.Notes))
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	info := autoModeConfigInfo(cfg)
	result.Config = &info
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
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	info := autoModeConfigInfo(cfg)
	result.Config = &info
	result.Success = true
	d.sendToClient(client, result)
}
