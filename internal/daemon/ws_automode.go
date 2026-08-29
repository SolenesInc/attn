package daemon

import (
	"context"
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

func (d *Daemon) handleAutoModeModelSet(client *wsClient, msg *protocol.AutoModeModelSetMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeModelSet, "automode_model_set is missing a request id")
		return
	}
	result := protocol.AutoModeModelSetResultMessage{
		Event:     protocol.EventAutoModeModelSetResult,
		RequestID: requestID,
	}
	cfg, err := d.store.SetAutoModeModels(msg.Models, time.Now())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	d.logf("automode: models are now %v", cfg.Models)
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	info := autoModeConfigInfo(cfg)
	result.Config = &info
	result.Success = true
	d.sendToClient(client, result)
}

const autoModeModelsPlugin = "attn-pi"

// pi is asked about every provider at once, each ask a process: five providers
// measured at 0.53s together, so this is a tripwire, not a deadline.
const autoModeModelsTimeout = 20 * time.Second

type pluginCatalogModel struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
}

type pluginModelProvider struct {
	Provider  string               `json:"provider"`
	Ready     bool                 `json:"ready"`
	Detail    string               `json:"detail,omitempty"`
	CheckedAt int                  `json:"checkedAt,omitempty"`
	Models    []pluginCatalogModel `json:"models"`
}

type pluginAvailableModels struct {
	Providers []pluginModelProvider `json:"providers"`
	Problem   string                `json:"problem,omitempty"`
}

func (d *Daemon) handleAutoModeModels(client *wsClient, msg *protocol.AutoModeModelsMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAutoModeModels, "automode_models is missing a request id")
		return
	}
	go d.answerAutoModeModels(client, requestID)
}

func (d *Daemon) answerAutoModeModels(client *wsClient, requestID string) {
	result := protocol.AutoModeModelsResultMessage{
		Event:     protocol.EventAutoModeModelsResult,
		RequestID: requestID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), autoModeModelsTimeout)
	defer cancel()

	var answer pluginAvailableModels
	if err := d.callPlugin(ctx, autoModeModelsPlugin, "automode.models", struct{}{}, &answer); err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	providers := make([]protocol.AutoModeModelProvider, 0, len(answer.Providers))
	for _, provider := range answer.Providers {
		models := make([]protocol.AutoModeCatalogModel, 0, len(provider.Models))
		for _, model := range provider.Models {
			entry := protocol.AutoModeCatalogModel{ID: model.ID}
			if model.Name != "" {
				entry.Name = protocol.Ptr(model.Name)
			}
			if model.ContextWindow > 0 {
				entry.ContextWindow = protocol.Ptr(model.ContextWindow)
			}
			models = append(models, entry)
		}
		out := protocol.AutoModeModelProvider{
			Provider: provider.Provider,
			Ready:    provider.Ready,
			Models:   models,
		}
		if provider.Detail != "" {
			out.Detail = protocol.Ptr(provider.Detail)
		}
		if provider.CheckedAt > 0 {
			out.CheckedAt = protocol.Ptr(provider.CheckedAt)
		}
		providers = append(providers, out)
	}
	if answer.Problem != "" {
		result.Problem = protocol.Ptr(answer.Problem)
	}
	result.Providers = providers
	result.Success = true
	d.sendToClient(client, result)
}
