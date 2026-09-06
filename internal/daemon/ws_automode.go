package daemon

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
)

// Only a human adds a rule or a host, so adding is the app's alone. Removal and the policy
// answer on both surfaces so the CLI can undo; a shipped forbidden rule keeps a session out.

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
	d.logf("automode: promoted proposal %d (%s)", proposal.ID,
		automode.DescribeProposal(proposal.Kind, proposal.Value))
	d.announceAutoModeConfig(cfg)
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


const autoModePluginName = "attn-pi"

// The driver hosts the proxy, so a host change has to reach it now rather than at the
// next launch. A driver without the method is a driver with no proxy: log and move on.
const autoModePolicyChangedTimeout = 5 * time.Second

type autoModePolicyChangedParams struct {
	Network protocol.AutoModeNetworkInfo `json:"network"`
}

func (d *Daemon) announceAutoModeConfig(cfg automode.Config) {
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	network := autoModeConfigInfo(cfg).Network
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), autoModePolicyChangedTimeout)
		defer cancel()
		var ignored struct{}
		if err := d.callPlugin(ctx, autoModePluginName, "automode.policy_changed",
			autoModePolicyChangedParams{Network: network}, &ignored); err != nil {
			d.logf("automode: telling %s the policy changed: %v", autoModePluginName, err)
		}
	}()
}

type autoModeConfigEdit func() (automode.Config, error)

func (d *Daemon) editAutoModeConfigWS(client *wsClient, cmd, requestID string, edit autoModeConfigEdit) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		d.sendCommandError(client, cmd, cmd+" is missing a request id")
		return
	}
	result := protocol.AutoModeConfigResultMessage{
		Event:     protocol.EventAutoModeConfigResult,
		RequestID: requestID,
	}
	cfg, err := edit()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.logf("automode: %s", cmd)
	d.announceAutoModeConfig(cfg)
	info := autoModeConfigInfo(cfg)
	result.Config = &info
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) editAutoModeConfigUnix(conn net.Conn, cmd string, edit autoModeConfigEdit) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	cfg, err := edit()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.logf("automode: %s", cmd)
	d.announceAutoModeConfig(cfg)
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok: true,
		AutomodeConfigResult: &protocol.AutoModeConfigResult{
			Config: autoModeConfigInfo(cfg),
		},
	})
}

func (d *Daemon) addAutoModeRule(msg *protocol.AutoModeRuleAddMessage) autoModeConfigEdit {
	return func() (automode.Config, error) {
		return d.store.AddAutoModeRule(automode.Rule{
			Pattern:       autoModeRuleTokens(msg.Pattern),
			Decision:      strings.TrimSpace(protocol.Deref(msg.Decision)),
			Justification: strings.TrimSpace(protocol.Deref(msg.Justification)),
		}, time.Now())
	}
}

func (d *Daemon) removeAutoModeRule(msg *protocol.AutoModeRuleRemoveMessage) autoModeConfigEdit {
	return func() (automode.Config, error) {
		return d.store.RemoveAutoModeRule(autoModeRuleTokens(msg.Pattern), time.Now())
	}
}

func (d *Daemon) addAutoModeHost(msg *protocol.AutoModeHostAddMessage) autoModeConfigEdit {
	return func() (automode.Config, error) {
		return d.store.AddAutoModeHost(automode.HostAmendment{
			Host:     strings.TrimSpace(msg.Host),
			Decision: strings.TrimSpace(msg.Decision),
		}, time.Now())
	}
}

func (d *Daemon) removeAutoModeHost(msg *protocol.AutoModeHostRemoveMessage) autoModeConfigEdit {
	return func() (automode.Config, error) {
		return d.store.RemoveAutoModeHost(automode.HostAmendment{
			Host:     strings.TrimSpace(msg.Host),
			Decision: strings.TrimSpace(msg.Decision),
		}, time.Now())
	}
}

func (d *Daemon) setAutoModePolicy(msg *protocol.AutoModePolicySetMessage) autoModeConfigEdit {
	return func() (automode.Config, error) {
		return d.store.SetAutoModePolicy(
			trimmedOption(msg.ApprovalPolicy), trimmedOption(msg.SandboxMode), time.Now())
	}
}

func trimmedOption(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func (d *Daemon) handleAutoModeRuleAdd(client *wsClient, msg *protocol.AutoModeRuleAddMessage) {
	d.editAutoModeConfigWS(client, protocol.CmdAutoModeRuleAdd, msg.RequestID, d.addAutoModeRule(msg))
}

func (d *Daemon) handleAutoModeRuleRemoveWS(client *wsClient, msg *protocol.AutoModeRuleRemoveMessage) {
	d.editAutoModeConfigWS(client, protocol.CmdAutoModeRuleRemove,
		protocol.Deref(msg.RequestID), d.removeAutoModeRule(msg))
}

func (d *Daemon) handleAutoModeRuleRemove(conn net.Conn, msg *protocol.AutoModeRuleRemoveMessage) {
	d.editAutoModeConfigUnix(conn, protocol.CmdAutoModeRuleRemove, d.removeAutoModeRule(msg))
}

func (d *Daemon) handleAutoModeHostAdd(client *wsClient, msg *protocol.AutoModeHostAddMessage) {
	d.editAutoModeConfigWS(client, protocol.CmdAutoModeHostAdd, msg.RequestID, d.addAutoModeHost(msg))
}

func (d *Daemon) handleAutoModeHostRemoveWS(client *wsClient, msg *protocol.AutoModeHostRemoveMessage) {
	d.editAutoModeConfigWS(client, protocol.CmdAutoModeHostRemove,
		protocol.Deref(msg.RequestID), d.removeAutoModeHost(msg))
}

func (d *Daemon) handleAutoModeHostRemove(conn net.Conn, msg *protocol.AutoModeHostRemoveMessage) {
	d.editAutoModeConfigUnix(conn, protocol.CmdAutoModeHostRemove, d.removeAutoModeHost(msg))
}

func (d *Daemon) handleAutoModePolicySetWS(client *wsClient, msg *protocol.AutoModePolicySetMessage) {
	d.editAutoModeConfigWS(client, protocol.CmdAutoModePolicySet,
		protocol.Deref(msg.RequestID), d.setAutoModePolicy(msg))
}

func (d *Daemon) handleAutoModePolicySet(conn net.Conn, msg *protocol.AutoModePolicySetMessage) {
	d.editAutoModeConfigUnix(conn, protocol.CmdAutoModePolicySet, d.setAutoModePolicy(msg))
}
