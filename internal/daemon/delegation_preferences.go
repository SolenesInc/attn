package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var errMissingRequestID = errors.New("missing request id")

func (d *Daemon) delegationHarnesses() []protocol.DelegationHarness {
	result := []protocol.DelegationHarness{}
	for _, name := range agentdriver.List() {
		driver := agentdriver.Get(name)
		caps := agentdriver.EffectiveCapabilities(driver)
		if !caps.HasInitialPrompt {
			continue
		}
		result = append(result, protocol.DelegationHarness{ID: name, Name: driver.DisplayName(), Available: isAgentExecutableAvailable(driver.ResolveExecutable(d.store.GetSetting(executableSettingKey(name))), driver.DefaultExecutable()), ModelPin: caps.HasModelPin, EffortPin: caps.HasEffortPin, Discovery: supportsModelDiscovery(driver)})
	}
	for _, driver := range d.ensurePluginRegistry().registeredDrivers() {
		if !driver.Capabilities["initial_prompt"] {
			continue
		}
		plugin := d.ensurePluginRegistry().get(driver.PluginName)
		if plugin == nil {
			continue
		}
		health, _, _ := plugin.healthSnapshot()
		result = append(result, protocol.DelegationHarness{ID: driver.Agent, Name: driver.Agent, Available: health != agentdriver.HealthUnhealthy, ModelPin: driver.Capabilities["model_pin"], EffortPin: driver.Capabilities["effort_pin"], Discovery: driver.Capabilities["model_discovery"]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (d *Daemon) delegationPreferencesResult(requestID string, cfg delegationprefs.Config) protocol.DelegationPreferencesResultMessage {
	return protocol.DelegationPreferencesResultMessage{
		Event:         protocol.EventDelegationPreferencesResult,
		RequestID:     requestID,
		Success:       true,
		Preferences:   &cfg,
		ExpandedRoles: append(prompts.ExpandDelegationRoles(cfg.Roles), prompts.ExpandDelegationRoles(prompts.DelegationRoleTemplates())...),
		Harnesses:     d.delegationHarnesses(),
		Templates:     prompts.DelegationRoleTemplates(),
	}
}

func delegationPreferencesFailure(requestID string, err error) protocol.DelegationPreferencesResultMessage {
	return protocol.DelegationPreferencesResultMessage{Event: protocol.EventDelegationPreferencesResult, RequestID: requestID, Error: protocol.Ptr(err.Error())}
}

func (d *Daemon) handleDelegationPreferencesGet(client *wsClient, msg *protocol.DelegationPreferencesGetMessage) {
	if strings.TrimSpace(msg.RequestID) == "" {
		d.sendToClient(client, delegationPreferencesFailure(msg.RequestID, errMissingRequestID))
		return
	}
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		d.sendToClient(client, delegationPreferencesFailure(msg.RequestID, err))
		return
	}
	d.sendToClient(client, d.delegationPreferencesResult(msg.RequestID, cfg))
}

func (d *Daemon) handleDelegationPreferencesSave(client *wsClient, msg *protocol.DelegationPreferencesSaveMessage) {
	if strings.TrimSpace(msg.RequestID) == "" {
		d.sendToClient(client, delegationPreferencesFailure(msg.RequestID, errMissingRequestID))
		return
	}
	install := protocol.Deref(msg.InstallWorkflowSkill)
	current, err := d.store.GetDelegationPreferences()
	var installedPaths []string
	if err == nil {
		err = delegationprefs.Validate(msg.Preferences)
	}
	if err == nil && !install {
		err = requireWorkflowSkillInstall(current, msg.Preferences)
	}
	if err == nil && install {
		if !msg.Preferences.WorkflowSkillEnabled {
			err = fmt.Errorf("the install action must save workflow_skill_enabled")
		} else if msg.Preferences.Revision != current.Revision {
			err = delegationprefs.ErrConflict
		} else {
			var harnesses []string
			for _, harness := range d.delegationHarnesses() {
				if harness.Available {
					harnesses = append(harnesses, harness.ID)
				}
			}
			var attempted bool
			installedPaths, attempted, err = agentdriver.EnsureWorkflowSkillsInstalled(harnesses)
			if err == nil && !attempted {
				err = fmt.Errorf("attn-workflow installation is disabled for instance %q; saved preferences were not changed", config.InstanceLabel())
			} else if err == nil && len(installedPaths) == 0 {
				err = fmt.Errorf("no available harness has a supported attn-workflow skill directory")
			}
		}
	}
	var saved store.DelegationPreferencesRevision
	if err == nil {
		saved, err = d.store.SaveDelegationPreferences(msg.Preferences, store.DelegationPreferencesNote{})
	}
	if err != nil {
		result := delegationPreferencesFailure(msg.RequestID, err)
		result.WorkflowSkillPaths = installedPaths
		d.sendToClient(client, result)
		return
	}
	d.publishFact(FactDelegationPreferencesChanged, "preferences", nil)
	result := d.delegationPreferencesResult(msg.RequestID, saved.Config)
	result.WorkflowSkillPaths = installedPaths
	d.sendToClient(client, result)
}

func (d *Daemon) handleDelegationPreferencesRollbackWS(client *wsClient, msg *protocol.DelegationPreferencesRollbackMessage) {
	requestID := protocol.Deref(msg.RequestID)
	if strings.TrimSpace(requestID) == "" {
		d.sendToClient(client, delegationPreferencesFailure(requestID, errMissingRequestID))
		return
	}
	restored, err := d.rollbackDelegationPreferences(msg)
	if err != nil {
		d.sendToClient(client, delegationPreferencesFailure(requestID, err))
		return
	}
	d.sendToClient(client, d.delegationPreferencesResult(requestID, restored.Config))
}

func (d *Daemon) handleDelegationPreferencesShow(conn net.Conn) {
	history, err := d.store.DelegationPreferencesHistory(1)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	live := store.DelegationPreferencesRevision{Config: delegationprefs.Defaults()}
	if len(history) > 0 {
		live = history[0]
	}
	d.replyDelegationPreferencesRevision(conn, live)
}

func (d *Daemon) handleDelegationPreferencesCommit(conn net.Conn, msg *protocol.DelegationPreferencesCommitMessage) {
	current, err := d.store.GetDelegationPreferences()
	if err == nil {
		err = requireWorkflowSkillInstall(current, msg.Preferences)
	}
	var saved store.DelegationPreferencesRevision
	if err == nil {
		saved, err = d.store.SaveDelegationPreferences(msg.Preferences, delegationPreferencesNote(msg.SourceSession, msg.Message))
	}
	if err != nil {
		d.replyDelegationPreferencesError(conn, err)
		return
	}
	d.publishFact(FactDelegationPreferencesChanged, "preferences", nil)
	d.replyDelegationPreferencesRevision(conn, saved)
}

func (d *Daemon) handleDelegationPreferencesHistory(conn net.Conn, msg *protocol.DelegationPreferencesHistoryMessage) {
	limit := protocol.Deref(msg.Limit)
	if limit <= 0 {
		limit = 20
	}
	history, err := d.store.DelegationPreferencesHistory(limit)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result := protocol.DelegationPreferencesHistoryResult{Revisions: make([]protocol.DelegationPreferencesRevision, 0, len(history))}
	for _, revision := range history {
		result.Revisions = append(result.Revisions, delegationPreferencesRevisionWire(revision))
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DelegationPreferencesHistory: &result})
}

func (d *Daemon) handleDelegationPreferencesRollback(conn net.Conn, msg *protocol.DelegationPreferencesRollbackMessage) {
	restored, err := d.rollbackDelegationPreferences(msg)
	if err != nil {
		d.replyDelegationPreferencesError(conn, err)
		return
	}
	d.replyDelegationPreferencesRevision(conn, restored)
}

func (d *Daemon) rollbackDelegationPreferences(msg *protocol.DelegationPreferencesRollbackMessage) (store.DelegationPreferencesRevision, error) {
	restored, err := d.store.RollbackDelegationPreferences(msg.Revision, msg.ExpectedRevision, delegationPreferencesNote(msg.SourceSession, msg.Message))
	if err == nil {
		d.publishFact(FactDelegationPreferencesChanged, "preferences", nil)
	}
	return restored, err
}

func requireWorkflowSkillInstall(current, next delegationprefs.Config) error {
	if next.WorkflowSkillEnabled && !current.WorkflowSkillEnabled {
		return errors.New("maintained Attn roles need the attn-workflow skill; install it with Add Attn roles in Settings > Delegation")
	}
	return nil
}

func delegationPreferencesNote(sourceSession, message *string) store.DelegationPreferencesNote {
	return store.DelegationPreferencesNote{SourceSession: strings.TrimSpace(protocol.Deref(sourceSession)), Message: strings.TrimSpace(protocol.Deref(message))}
}

func (d *Daemon) replyDelegationPreferencesRevision(conn net.Conn, revision store.DelegationPreferencesRevision) {
	wire := delegationPreferencesRevisionWire(revision)
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DelegationPreferencesRevision: &wire})
}

func (d *Daemon) replyDelegationPreferencesError(conn net.Conn, err error) {
	response := protocol.Response{Ok: false, Error: protocol.Ptr(err.Error())}
	if errors.Is(err, delegationprefs.ErrConflict) {
		response.ErrorCode = protocol.Ptr(protocol.ErrorCodeConflict)
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func delegationPreferencesRevisionWire(revision store.DelegationPreferencesRevision) protocol.DelegationPreferencesRevision {
	wire := protocol.DelegationPreferencesRevision{Preferences: revision.Config, Restores: revision.Restores, Changes: []string{"history starts here"}}
	if revision.Previous != nil {
		wire.Changes = delegationprefs.Changes(*revision.Previous, revision.Config)
	}
	if revision.SourceSession != "" {
		wire.SourceSession = &revision.SourceSession
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

func supportsModelDiscovery(driver agentdriver.Driver) bool {
	_, ok := driver.(agentdriver.ModelDiscoverer)
	return ok
}

func usesDelegationPreferences(msg *protocol.DelegateMessage) bool {
	return msg.Role != nil || msg.Choice != nil || protocol.Deref(msg.Fallback)
}

func (d *Daemon) ensureDelegationWorkflowSkill(resolved *delegationprefs.Resolved) error {
	harness := resolved.Selection.Harness
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		return err
	}
	if !cfg.WorkflowSkillEnabled {
		return nil
	}
	paths, attempted, err := agentdriver.EnsureWorkflowSkillsInstalled([]string{harness})
	if err != nil {
		return fmt.Errorf("sync attn-workflow before delegated role launch: %w", err)
	}
	if !attempted {
		d.logf("skipping user-global attn-workflow skill sync for instance %q", config.InstanceLabel())
		return nil
	}
	if len(paths) == 0 && resolved.Builtin != nil {
		return fmt.Errorf("harness %q has no supported attn-workflow skill directory", harness)
	}
	return nil
}

func (d *Daemon) resolveDelegationPreferences(msg *protocol.DelegateMessage) (*delegationprefs.Resolved, error) {
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
	if _, err := d.resolveDelegationAgent("", &s.Harness); err != nil {
		return nil, err
	}
	if s.Provider != "" {
		if _, ok := d.ensurePluginRegistry().driver(s.Harness); !ok {
			return nil, fmt.Errorf("harness %q uses its configured provider; provider selection is supported by plugin harnesses", s.Harness)
		}
	}
	if err := d.validateDelegationModelEffort(s.Harness, s.Model, s.Effort); err != nil {
		return nil, err
	}
	if s.Model != "" {
		catalog, err := d.discoverDelegationModels(context.Background(), s.Harness)
		if err != nil {
			return nil, fmt.Errorf("validate selected model: %w", err)
		}
		for _, m := range catalog.Models {
			if m.ID != s.Model || m.Provider != s.Provider {
				continue
			}
			if m.Access == protocol.ModelCapabilitySupportUnsupported {
				return nil, fmt.Errorf("model %q is unavailable: %s", s.Model, m.Detail)
			}
			if s.Effort != "" && (m.EffortSupport == protocol.ModelCapabilitySupportUnsupported || (len(m.EffortLevels) > 0 && !slices.Contains(m.EffortLevels, s.Effort))) {
				return nil, fmt.Errorf("model %q does not support effort %q", s.Model, s.Effort)
			}
			break
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
