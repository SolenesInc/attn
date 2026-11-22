package daemon

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attnskill"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) delegationHarnesses() []protocol.Harness {
	result := []protocol.Harness{}
	for _, h := range d.harnesses() {
		facts := h.Facts()
		if !facts.Options.InitialPrompt {
			continue
		}
		health := h.Health(d.life.Context())
		if !health.Offered {
			continue
		}
		result = append(result, harnessRecord(d.life.Context(), h))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (d *Daemon) handleDelegationPreferencesGet(client *wsClient, msg *protocol.DelegationPreferencesGetMessage) {
	result := protocol.DelegationPreferencesResultMessage{Event: protocol.EventDelegationPreferencesResult, RequestID: msg.RequestID}
	if strings.TrimSpace(msg.RequestID) == "" {
		result.Error = protocol.Ptr("missing request id")
		d.sendToClient(client, result)
		return
	}
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Preferences = &cfg
		result.ExpandedRoles = append(prompts.ExpandDelegationRoles(cfg.Roles), prompts.ExpandDelegationRoles(prompts.DelegationRoleTemplates())...)
		result.Harnesses = d.delegationHarnesses()
		result.Templates = prompts.DelegationRoleTemplates()
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleDelegationPreferencesSave(client *wsClient, msg *protocol.DelegationPreferencesSaveMessage) {
	result := protocol.DelegationPreferencesResultMessage{Event: protocol.EventDelegationPreferencesResult, RequestID: msg.RequestID}
	if strings.TrimSpace(msg.RequestID) == "" {
		result.Error = protocol.Ptr("missing request id")
		d.sendToClient(client, result)
		return
	}
	current, err := d.store.GetDelegationPreferences()
	var installedPaths []string
	if err == nil {
		installedPaths, err = d.installAttnRoles(current, msg.Preferences, protocol.Deref(msg.InstallWorkflowSkill))
	}
	var cfg delegationprefs.Config
	if err == nil {
		var saved store.DelegationPreferencesRevision
		saved, err = d.store.SaveDelegationPreferences(msg.Preferences, store.DelegationPreferencesNote{Origin: string(protocol.DelegationPreferencesOriginSettings)})
		cfg = saved.Config
	}
	result.WorkflowSkillPaths = installedPaths
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Preferences = &cfg
		result.ExpandedRoles = append(prompts.ExpandDelegationRoles(cfg.Roles), prompts.ExpandDelegationRoles(prompts.DelegationRoleTemplates())...)
		result.Harnesses = d.delegationHarnesses()
		result.Templates = prompts.DelegationRoleTemplates()
		if !protocol.Deref(msg.InstallWorkflowSkill) {
			d.ensureEnabledWorkflowSkills()
		}
		d.publishFact(FactDelegationPreferencesChanged, "preferences", nil)
	}
	d.sendToClient(client, result)
}

func (d *Daemon) installAttnRoles(current, next delegationprefs.Config, install bool) ([]string, error) {
	if !install {
		if next.WorkflowSkillEnabled && !current.WorkflowSkillEnabled {
			return nil, fmt.Errorf("enabling attn-workflow needs the explicit install: attn delegate roles install, or Add Attn roles in Settings > Delegation")
		}
		return nil, nil
	}
	if !next.WorkflowSkillEnabled {
		return nil, fmt.Errorf("the install action must save workflow_skill_enabled")
	}
	if next.Revision != current.Revision {
		return nil, delegationprefs.ErrConflict
	}
	if err := delegationprefs.Validate(next); err != nil {
		return nil, err
	}
	var harnesses []string
	for _, harness := range d.delegationHarnesses() {
		if harness.Available() {
			harnesses = append(harnesses, harness.ID)
		}
	}
	paths, attempted, err := attnskill.EnsureWorkflowSkillsInstalled(d.workflowSkillsDirs(harnesses))
	if err != nil {
		return paths, err
	}
	if !attempted {
		return paths, fmt.Errorf("attn-workflow installation is disabled for instance %q; saved preferences were not changed", config.InstanceLabel())
	}
	if len(paths) == 0 {
		return paths, fmt.Errorf("no available harness has a supported attn-workflow skill directory")
	}
	return paths, nil
}

func cliDelegationPreferencesNote(message *string) store.DelegationPreferencesNote {
	return store.DelegationPreferencesNote{Origin: string(protocol.DelegationPreferencesOriginCli), Message: strings.TrimSpace(protocol.Deref(message))}
}

func delegationPreferencesRevisionWire(revision store.DelegationPreferencesRevision) protocol.DelegationPreferencesRevision {
	wire := protocol.DelegationPreferencesRevision{Preferences: revision.Config, Restores: revision.Restores, Changes: []string{"history starts here"}}
	if revision.Previous != nil {
		wire.Changes = delegationprefs.Changes(*revision.Previous, revision.Config)
	}
	if revision.Origin != "" {
		wire.Origin = protocol.Ptr(protocol.DelegationPreferencesOrigin(revision.Origin))
	}
	if revision.Message != "" {
		wire.Message = &revision.Message
	}
	if !revision.CreatedAt.IsZero() {
		wire.CreatedAt = protocol.Ptr(revision.CreatedAt.UTC().Format(time.RFC3339))
	}
	return wire
}

func (d *Daemon) projectDelegationPreferencesChanged() {
	d.projectSnapshot(protocol.EventDelegationPreferencesChanged, func() {
		cfg, err := d.store.GetDelegationPreferences()
		if err != nil {
			d.logf("delegation preferences snapshot: %v", err)
			return
		}
		d.broadcastMessage(protocol.DelegationPreferencesChangedMessage{Event: protocol.EventDelegationPreferencesChanged, Revision: cfg.Revision})
	})
}

func (d *Daemon) selectsProviders(id string) bool {
	h, err := d.harness(id)
	return err == nil && h.Facts().Options.Provider
}

func usesDelegationPreferences(msg *protocol.DelegateMessage) bool {
	return msg.Role != nil || msg.Choice != nil || protocol.Deref(msg.Fallback)
}

func (d *Daemon) ensureDelegationWorkflowSkill(harness string, required bool) error {
	paths, attempted, err := attnskill.EnsureWorkflowSkillsInstalled(d.workflowSkillsDirs([]string{harness}))
	if err != nil {
		return fmt.Errorf("sync attn-workflow before delegated role launch: %w", err)
	}
	if !attempted {
		d.logf("skipping user-global attn-workflow skill sync for instance %q", config.InstanceLabel())
		return nil
	}
	if len(paths) == 0 && required {
		return fmt.Errorf("harness %q has no supported attn-workflow skill directory", harness)
	}
	return nil
}

func (d *Daemon) resolveDelegationPreferences(msg *protocol.DelegateMessage, host string) (*delegationprefs.Resolved, error) {
	if !usesDelegationPreferences(msg) {
		if msg.Provider != nil {
			return nil, fmt.Errorf("--provider requires --role or --fallback; direct plugin delegation uses provider/model")
		}
		return nil, nil
	}
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	expanded := prompts.ExpandDelegationPreferences(cfg)
	resolved, err := delegationprefs.Resolve(expanded, delegationprefs.Request{Role: protocol.Deref(msg.Role), Choice: protocol.Deref(msg.Choice), Fallback: protocol.Deref(msg.Fallback), Harness: msg.Agent, Provider: msg.Provider, Model: msg.Model, Effort: msg.Effort})
	if err != nil {
		return nil, err
	}
	s := resolved.Selection
	d.awaitHarness(s.Harness)
	if _, err := d.resolveDelegationAgent("", &s.Harness); err != nil {
		return nil, err
	}
	if s.Provider != "" && !d.selectsProviders(s.Harness) {
		return nil, fmt.Errorf("harness %q uses its configured provider; provider selection is supported by plugin harnesses", s.Harness)
	}
	if err := d.validateHarnessModelEffort(s.Harness, s.Model, s.Effort); err != nil {
		return nil, err
	}
	if s.Model != "" {
		m, listed, err := listedRechecked(func(refresh bool) (protocol.HarnessModel, bool, bool, error) {
			catalog, err := Call[protocol.AgentCatalogResult](context.Background(), d.router(), protocol.AgentCatalogMessage{Cmd: protocol.CmdAgentCatalog, Host: host, ModelsFor: &s.Harness, RefreshModels: protocol.Ptr(refresh)})
			if err != nil {
				return protocol.HarnessModel{}, false, false, fmt.Errorf("validate selected model: %w", err)
			}
			i := slices.IndexFunc(catalog.Models, func(m protocol.HarnessModel) bool { return m.ID == s.Model && m.Provider == s.Provider })
			if i < 0 {
				return protocol.HarnessModel{}, false, true, nil
			}
			return catalog.Models[i], true, true, nil
		}, signedOut)
		if err != nil {
			return nil, err
		}
		if listed && signedOut(m) {
			return nil, fmt.Errorf("model %q is unavailable: %s", s.Model, m.Detail)
		}
		if listed && s.Effort != "" && (m.EffortSupport == protocol.ModelCapabilitySupportUnsupported || (len(m.EffortLevels) > 0 && !slices.Contains(m.EffortLevels, s.Effort))) {
			return nil, fmt.Errorf("model %q does not support effort %q", s.Model, s.Effort)
		}
	}
	latest, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	if !latest.Enabled || latest.Revision != cfg.Revision {
		return nil, delegationprefs.ErrConflict
	}
	return &resolved, nil
}
