package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func automationDefinitionYAML(def store.AutomationDefinition) (string, error) {
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		return "", fmt.Errorf("parse stored definition %s: %w", def.ID, err)
	}
	rendered, err := automation.MarshalDefinitionYAML(spec)
	if err != nil {
		return "", fmt.Errorf("render definition %s: %w", def.ID, err)
	}
	return string(rendered), nil
}

const automationRunSummaryListCap = 100

func (d *Daemon) actionAutomationDefinitionsGet(msg *protocol.AutomationDefinitionsGetMessage) protocol.AutomationDefinitionsResultMessage {
	result := protocol.AutomationDefinitionsResultMessage{
		Event:     protocol.EventAutomationDefinitionsResult,
		RequestID: msg.RequestID,
	}
	definitions, err := d.store.ListAutomationDefinitions()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	lastRuns, err := d.store.LatestAutomationRunPerDefinition()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	result.Definitions = make([]protocol.AutomationDefinitionSummary, len(definitions))
	for i := range definitions {
		var lastRun *store.AutomationRunWithOccurrenceKey
		if run, ok := lastRuns[definitions[i].ID]; ok {
			lastRun = &run
		}
		result.Definitions[i] = d.buildAutomationDefinitionSummary(definitions[i], lastRun)
	}
	return result
}

func (d *Daemon) actionAutomationDefinitionGet(msg *protocol.AutomationDefinitionGetMessage) protocol.AutomationDefinitionResultMessage {
	result := protocol.AutomationDefinitionResultMessage{
		Event:     protocol.EventAutomationDefinitionResult,
		RequestID: msg.RequestID,
	}
	if msg.DefinitionID == "" {
		template, err := automation.StarterTemplateYAML()
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			return result
		}
		starterJSON, err := json.Marshal(automation.StarterDefinition)
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			return result
		}
		result.Success = true
		result.SpecYaml = protocol.Ptr(string(template))
		result.SpecJson = protocol.Ptr(string(starterJSON))
		return result
	}
	definition, err := d.store.GetAutomationDefinition(msg.DefinitionID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	if definition == nil {
		result.Error = protocol.Ptr("automation definition not found")
		return result
	}
	specYAML, err := automationDefinitionYAML(*definition)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	result.SpecYaml = protocol.Ptr(specYAML)
	result.SpecJson = protocol.Ptr(definition.SpecJSON)
	summary := d.buildAutomationDefinitionSummary(*definition, nil)
	result.Definition = &summary
	return result
}

func (d *Daemon) actionAutomationRunsGet(msg *protocol.AutomationRunsGetMessage) protocol.AutomationRunsResultMessage {
	result := protocol.AutomationRunsResultMessage{
		Event:        protocol.EventAutomationRunsResult,
		RequestID:    msg.RequestID,
		DefinitionID: msg.DefinitionID,
	}
	runs, err := d.store.ListAutomationRunsWithOccurrenceKeys(msg.DefinitionID, automationRunSummaryListCap+1)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	if len(runs) > automationRunSummaryListCap {
		runs = runs[:automationRunSummaryListCap]
		result.Truncated = protocol.Ptr(true)
	}
	result.Success = true
	result.Runs = make([]protocol.AutomationRunSummary, len(runs))
	for i := range runs {
		result.Runs[i] = d.automationRunSummary(runs[i])
	}
	return result
}

func (d *Daemon) actionAutomationValidate(msg *protocol.AutomationValidateMessage) protocol.AutomationValidateResultMessage {
	result := protocol.AutomationValidateResultMessage{
		Event:     protocol.EventAutomationValidateResult,
		RequestID: msg.RequestID,
	}
	if _, _, err := d.validateAutomationSpec(msg.DefinitionYaml); err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	return result
}

// actionAutomationApply is the one apply path for both transports. The socket/CLI path
// omits expected_id/expected_revision, so no guard is enforced; the WS editor sends both.
func (d *Daemon) actionAutomationApply(ctx context.Context, msg *protocol.AutomationApplyMessage) protocol.AutomationApplyResultMessage {
	result := protocol.AutomationApplyResultMessage{
		Event:     protocol.EventAutomationApplyResult,
		RequestID: msg.RequestID,
	}
	definition, err := d.automationApplyWithGuards(ctx, msg.DefinitionYaml, msg.ExpectedID, msg.ExpectedRevision)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		var refusal *automationRefusal
		if errors.As(err, &refusal) {
			result.ErrorCode = protocol.Ptr(refusal.Code)
		}
		return result
	}
	result.Success = true
	summary := d.buildAutomationDefinitionSummary(*definition, nil)
	result.Definition = &summary
	specYAML, err := automationDefinitionYAML(*definition)
	if err == nil {
		result.SpecYaml = protocol.Ptr(specYAML)
	}
	return result
}

func (d *Daemon) actionAutomationSetEnabled(ctx context.Context, msg *protocol.AutomationSetEnabledMessage) protocol.AutomationSetEnabledResultMessage {
	result := protocol.AutomationSetEnabledResultMessage{
		Event:     protocol.EventAutomationSetEnabledResult,
		RequestID: msg.RequestID,
	}
	definition, err := d.automationSetEnabled(ctx, msg.DefinitionID, msg.Enabled)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	summary := d.buildAutomationDefinitionSummary(*definition, nil)
	result.Definition = &summary
	return result
}

func (d *Daemon) actionAutomationDelete(ctx context.Context, msg *protocol.AutomationDeleteMessage) protocol.AutomationDeleteResultMessage {
	result := protocol.AutomationDeleteResultMessage{
		Event:     protocol.EventAutomationDeleteResult,
		RequestID: msg.RequestID,
	}
	if err := d.automationDelete(ctx, msg.DefinitionID); err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	return result
}

func (d *Daemon) actionAutomationCleanup(ctx context.Context, msg *protocol.AutomationCleanupMessage) protocol.AutomationCleanupResultMessage {
	result := protocol.AutomationCleanupResultMessage{
		Event:     protocol.EventAutomationCleanupResult,
		RequestID: msg.RequestID,
	}
	cleaned, keptDirty, keptActive, err := d.automationCleanup(ctx, msg.DefinitionID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	result.Cleaned = cleaned
	result.KeptDirty = keptDirty
	result.KeptActive = keptActive
	return result
}

func (d *Daemon) actionAutomationRun(ctx context.Context, msg *protocol.AutomationRunMessage) protocol.AutomationRunResultMessage {
	result := protocol.AutomationRunResultMessage{
		Event:     protocol.EventAutomationRunResult,
		RequestID: protocol.Ptr(msg.RequestID),
	}
	prURL := strings.TrimSpace(protocol.Deref(msg.PRURL))
	inputJSON := strings.TrimSpace(protocol.Deref(msg.InputJson))
	if prURL != "" && inputJSON != "" {
		result.Error = protocol.Ptr("pr_url and input_json are mutually exclusive")
		return result
	}
	var run *store.AutomationRun
	var err error
	if prURL != "" {
		run, err = d.automationRunPullRequest(ctx, msg.DefinitionID, msg.RequestID, prURL)
	} else {
		run, err = d.automationRun(ctx, msg.DefinitionID, msg.RequestID, protocol.Deref(msg.InputJson))
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	result.Success = true
	summary := d.automationRunSummary(store.AutomationRunWithOccurrenceKey{AutomationRun: *run})
	result.Run = &summary
	return result
}

func (d *Daemon) buildAutomationDefinitionSummary(def store.AutomationDefinition, lastRun *store.AutomationRunWithOccurrenceKey) protocol.AutomationDefinitionSummary {
	summary := protocol.AutomationDefinitionSummary{
		ID:        def.ID,
		Name:      def.Name,
		Enabled:   def.Enabled,
		Revision:  def.Revision,
		UpdatedAt: string(protocol.NewTimestamp(def.UpdatedAt)),
	}
	if lastRun != nil {
		runSummary := d.automationRunSummary(*lastRun)
		summary.LastRun = &runSummary
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		d.logf("automation definition summary parse %s: %v", def.ID, err)
		return summary
	}
	summary.TriggerType = spec.Trigger.Type
	if spec.Trigger.Schedule != nil {
		summary.ScheduleCron = protocol.Ptr(spec.Trigger.Schedule.Cron)
		summary.ScheduleTimeZone = protocol.Ptr(spec.Trigger.Schedule.TimeZone)
	}
	return summary
}

func (d *Daemon) automationRunSummary(run store.AutomationRunWithOccurrenceKey) protocol.AutomationRunSummary {
	summary := protocol.AutomationRunSummary{
		ID:            run.ID,
		DefinitionID:  run.DefinitionID,
		State:         run.State,
		TicketID:      protocol.Ptr(run.TicketID),
		SessionID:     protocol.Ptr(run.SessionID),
		PaneID:        protocol.Ptr(run.PaneID),
		CreatedAt:     string(protocol.NewTimestamp(run.CreatedAt)),
		UpdatedAt:     string(protocol.NewTimestamp(run.UpdatedAt)),
		OccurrenceKey: protocol.Ptr(run.OccurrenceKey),
	}
	if run.LastError != "" {
		summary.LastError = protocol.Ptr(run.LastError)
	}
	if run.CancelReason != "" {
		summary.CancelReason = protocol.Ptr(run.CancelReason)
	}
	if run.DeliveredAt != nil {
		summary.DeliveredAt = protocol.Ptr(string(protocol.NewTimestamp(*run.DeliveredAt)))
	}
	record := run.Provenance
	if record.RunID == "" {
		loaded, err := d.store.GetAutomationProvenanceRecord(run.ID)
		if err != nil {
			d.logf("automation run %s provenance: %v", run.ID, err)
		} else if loaded != nil {
			record = *loaded
		}
	}
	if record.RunID != "" {
		provenance, err := automationProvenance(record)
		if err != nil {
			d.logf("automation run %s provenance: %v", run.ID, err)
		}
		summary.Automation = provenance
	}
	return summary
}
