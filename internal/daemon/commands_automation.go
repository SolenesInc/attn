package daemon

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) automationCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"automation apply":    command.Typed(d.runAutomationApply),
		"automation validate": command.Typed(d.runAutomationValidate),
		"automation list":     command.Typed(d.runAutomationList),
		"automation show":     command.Typed(d.runAutomationShow),
		"automation set":      command.Typed(d.runAutomationSet),
		"automation enable": command.Typed(func(_ context.Context, c command.Caller, args command.AutomationEnableArgs) (any, error) {
			return d.runAutomationSetEnabled(c, args.ID, true)
		}),
		"automation disable": command.Typed(func(_ context.Context, c command.Caller, args command.AutomationDisableArgs) (any, error) {
			return d.runAutomationSetEnabled(c, args.ID, false)
		}),
		"automation run":     command.Typed(d.runAutomationRun),
		"automation runs":    command.Typed(d.runAutomationRuns),
		"automation cleanup": command.Typed(d.runAutomationCleanup),
		"automation delete":  command.Typed(d.runAutomationDelete),
	}
}

func (d *Daemon) automationScope(c command.Caller) (string, error) {
	profile, err := d.resolveGardenProfile(c.Session, c.Profile, "")
	return profile.ID, err
}

func automationFailed(errText *string) error {
	return errors.New(protocol.Deref(errText))
}

func automationID(id *int64) (int, error) {
	if id == nil || *id < 1 {
		return 0, command.Usage("{id} must be a positive number, as automation list prints it")
	}
	return int(*id), nil
}

func (d *Daemon) runAutomationApply(_ context.Context, c command.Caller, args command.AutomationApplyArgs) (any, error) {
	if args.Definition == "" {
		return nil, command.Usage("{definition} is required")
	}
	if args.DesktopName != "" && args.LaunchDesktop == "" {
		return nil, command.Refuse("desktop_name_needs_desktop", "{desktop_name} needs {launch_desktop} own or an empty slot")
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	definition, err := d.automationApplyWithGuards(d.life.Context(), args.Definition, scope, scope, nil, nil, &launchDesktopWrite{ref: optional(args.LaunchDesktop), name: optional(args.DesktopName)})
	var refusal *automationRefusal
	if errors.As(err, &refusal) {
		return nil, command.Refuse(refusal.Code, "%s", protocol.MentionedText(refusal.Err))
	}
	if err != nil {
		return nil, err
	}
	return d.buildAutomationDefinitionSummary(*definition, nil), nil
}

func (d *Daemon) runAutomationValidate(_ context.Context, _ command.Caller, args command.AutomationValidateArgs) (any, error) {
	if args.Definition == "" {
		return nil, command.Usage("{definition} is required")
	}
	result := d.actionAutomationValidate(&protocol.AutomationValidateMessage{DefinitionYaml: args.Definition})
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return protocol.AutomationValidateResult{Valid: true}, nil
}

func (d *Daemon) runAutomationList(_ context.Context, c command.Caller, _ command.AutomationListArgs) (any, error) {
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	result := d.actionAutomationDefinitionsGet(&protocol.AutomationDefinitionsGetMessage{}, scope)
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return protocol.AutomationListResult{Definitions: result.Definitions}, nil
}

func (d *Daemon) runAutomationShow(_ context.Context, c command.Caller, args command.AutomationShowArgs) (any, error) {
	definitionID, err := automationID(args.ID)
	if err != nil {
		return nil, err
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	result := d.actionAutomationDefinitionGet(&protocol.AutomationDefinitionGetMessage{DefinitionID: definitionID}, scope)
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return protocol.AutomationShowResult{Definition: *result.Definition, SpecYaml: protocol.Deref(result.SpecYaml)}, nil
}

func (d *Daemon) runAutomationSet(_ context.Context, c command.Caller, args command.AutomationSetArgs) (any, error) {
	if args.LaunchDesktop == "" {
		return nil, command.Usage("{launch_desktop} is required")
	}
	definitionID, err := automationID(args.ID)
	if err != nil {
		return nil, err
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	id := strconv.Itoa(definitionID)
	if d.automationOutOfScope(definitionID, scope) {
		return nil, fmt.Errorf("automation %q does not exist", id)
	}
	result := d.launchDesktopResult(protocol.CmdLaunchDesktopSet, "", string(protocol.LaunchDesktopKindAutomation), id, nil, &args.LaunchDesktop, optional(args.DesktopName))
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return result.Item, nil
}

func (d *Daemon) runAutomationSetEnabled(c command.Caller, id *int64, enabled bool) (any, error) {
	definitionID, err := automationID(id)
	if err != nil {
		return nil, err
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	result := d.actionAutomationSetEnabled(d.life.Context(), &protocol.AutomationSetEnabledMessage{DefinitionID: definitionID, Enabled: enabled}, scope)
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return result.Definition, nil
}

func (d *Daemon) runAutomationRun(_ context.Context, c command.Caller, args command.AutomationRunArgs) (any, error) {
	if args.PRURL != "" && len(args.Input) > 0 {
		return nil, command.Usage("{input} and {pr_url} are mutually exclusive")
	}
	id, err := automationID(args.ID)
	if err != nil {
		return nil, err
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	if d.automationOutOfScope(id, scope) {
		return nil, errors.New(automationNotFound(id))
	}
	requestID := args.RequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	var run *store.AutomationRun
	switch {
	case args.PRURL != "":
		run, err = d.automationRunPullRequest(d.life.Context(), id, requestID, args.PRURL, args.Wait)
	case len(args.Input) > 0:
		run, err = d.automationRun(d.life.Context(), id, requestID, string(args.Input), args.Wait)
	default:
		run, err = d.automationRun(d.life.Context(), id, requestID, "{}", args.Wait)
	}
	if err != nil {
		return nil, err
	}
	return d.automationRunSummary(store.AutomationRunWithOccurrenceKey{AutomationRun: *run}), nil
}

func (d *Daemon) runAutomationRuns(_ context.Context, c command.Caller, args command.AutomationRunsArgs) (any, error) {
	definitionID, err := automationID(args.ID)
	if err != nil {
		return nil, err
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	result := d.actionAutomationRunsGet(&protocol.AutomationRunsGetMessage{DefinitionID: definitionID}, scope)
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return protocol.AutomationRunsResult{DefinitionID: result.DefinitionID, Runs: result.Runs, Truncated: result.Truncated}, nil
}

func (d *Daemon) runAutomationCleanup(_ context.Context, c command.Caller, args command.AutomationCleanupArgs) (any, error) {
	definitionID, err := automationID(args.ID)
	if err != nil {
		return nil, err
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	result := d.actionAutomationCleanup(d.life.Context(), &protocol.AutomationCleanupMessage{DefinitionID: definitionID}, scope)
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return protocol.AutomationCleanupResult{Cleaned: nonNil(result.Cleaned), KeptDirty: nonNil(result.KeptDirty), KeptActive: nonNil(result.KeptActive)}, nil
}

func (d *Daemon) runAutomationDelete(_ context.Context, c command.Caller, args command.AutomationDeleteArgs) (any, error) {
	id, err := automationID(args.ID)
	if err != nil {
		return nil, err
	}
	scope, err := d.automationScope(c)
	if err != nil {
		return nil, err
	}
	result := d.actionAutomationDelete(d.life.Context(), &protocol.AutomationDeleteMessage{DefinitionID: id}, scope)
	if !result.Success {
		return nil, automationFailed(result.Error)
	}
	return protocol.AutomationDeleteResult{Deleted: id}, nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
