package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/automation"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type automationRefusal struct {
	Code string
	Err  error
}

func (r *automationRefusal) Error() string { return r.Err.Error() }
func (r *automationRefusal) Unwrap() error { return r.Err }

const (
	automationErrCodeRevisionConflict = "revision_conflict"
	automationErrCodeIDCollision      = "id_collision"
	automationErrCodeDeletedElsewhere = "deleted_elsewhere"
	automationErrCodeIDMismatch       = "id_mismatch"
	automationErrCodeValidation       = "validation"
)

// Strictly inside the frontend's 30s client timeout (useDaemonSocket.ts), so a flip after
// a reported failure is impossible.
const defaultWSAutomationMutationTimeout = 25 * time.Second

func (d *Daemon) wsAutomationMutationTimeoutDuration() time.Duration {
	if d.wsAutomationMutationTimeout > 0 {
		return d.wsAutomationMutationTimeout
	}
	return defaultWSAutomationMutationTimeout
}

func (d *Daemon) validateAutomationSpec(raw string) (automation.DefinitionSpec, []byte, error) {
	spec, canonical, err := automation.ParseDefinitionYAML([]byte(raw))
	if err != nil {
		return spec, nil, err
	}
	if _, err := d.resolveDelegationAgent("", protocol.Ptr(spec.Launch.Driver)); err != nil {
		return spec, nil, err
	}
	if err := d.validateDelegationModelEffort(spec.Launch.Driver, spec.Launch.Model, spec.Launch.Effort); err != nil {
		return spec, nil, err
	}
	if spec.Launch.Driver != "codex" && spec.Launch.Driver != "claude" {
		return spec, nil, fmt.Errorf("agent %q does not support automation automatic approval", spec.Launch.Driver)
	}
	for identity, source := range spec.Location.RepositorySources.Overrides {
		if _, err := attngit.ValidateLocalClone(source.Path, identity); err != nil {
			return spec, nil, fmt.Errorf("repository override %s: %w", identity, err)
		}
	}
	return spec, canonical, nil
}

func (d *Daemon) automationApply(raw string) (*store.AutomationDefinition, error) {
	return d.automationApplyWithGuards(context.Background(), raw, nil, nil)
}

// Guards key on POINTER PRESENCE, not zero value — nil is unguarded, and expectedRevision
// 0 means "creating". Both run inside automationMu, atomic with the pre-upsert read.
func (d *Daemon) automationApplyWithGuards(ctx context.Context, raw string, expectedID *string, expectedRevision *int) (*store.AutomationDefinition, error) {
	spec, canonical, err := d.validateAutomationSpec(raw)
	if err != nil {
		return nil, &automationRefusal{Code: automationErrCodeValidation, Err: err}
	}
	if expectedID != nil && *expectedID != "" && spec.ID != *expectedID {
		return nil, &automationRefusal{Code: automationErrCodeIDMismatch, Err: fmt.Errorf("definition id %q in the YAML does not match the definition being edited (%q) — apply is keyed on the id inside the YAML, so an id change must be made as a separate create", spec.ID, *expectedID)}
	}
	guard := func(existing *store.AutomationDefinition) error {
		if expectedRevision == nil {
			return nil
		}
		if *expectedRevision == 0 {
			if existing != nil && existing.DeletedAt == nil {
				return &automationRefusal{Code: automationErrCodeIDCollision, Err: fmt.Errorf("an automation with id %q already exists — edit it instead of creating a second one", spec.ID)}
			}
			return nil
		}
		if existing == nil || existing.Revision != *expectedRevision {
			return &automationRefusal{Code: automationErrCodeRevisionConflict, Err: errors.New("automation definition changed elsewhere — reload before saving")}
		}
		if existing.DeletedAt != nil {
			// An edit must never resurrect: delete already failed the pending runs and purged
			// bindings, so a silent Save would restart a cron the user deliberately deleted.
			return &automationRefusal{Code: automationErrCodeDeletedElsewhere, Err: fmt.Errorf("automation %q was deleted elsewhere while you were editing it — your changes were not saved; close this editor and use New if you want to bring it back", spec.ID)}
		}
		return nil
	}
	return d.automationApplyLocked(ctx, spec, canonical, guard)
}

func (d *Daemon) automationApplyLocked(ctx context.Context, spec automation.DefinitionSpec, canonical []byte, guard func(*store.AutomationDefinition) error) (*store.AutomationDefinition, error) {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("deadline exceeded waiting for an in-flight automation delivery: %w", err)
	}
	existing, err := d.store.GetAutomationDefinitionIncludingDeleted(spec.ID)
	if err != nil {
		return nil, err
	}
	if guard != nil {
		if err := guard(existing); err != nil {
			return nil, err
		}
	}
	definition, err := d.store.UpsertAutomationDefinition(spec.ID, spec.Name, string(canonical), time.Now())
	if err != nil {
		return definition, err
	}
	if err := d.rotateContinuityBindingsIfContractChanged(existing, spec, definition); err != nil {
		return definition, err
	}
	d.broadcastAutomationsChanged(spec.ID)
	if definition.Enabled {
		return definition, nil
	}
	return definition, d.cancelPendingAutomationRuns(spec.ID, store.AutomationCancelReasonDefinitionDisabled)
}

func (d *Daemon) rotateContinuityBindingsIfContractChanged(existing *store.AutomationDefinition, spec automation.DefinitionSpec, updated *store.AutomationDefinition) error {
	if existing == nil {
		return nil
	}
	rotate := existing.DeletedAt != nil
	if !rotate && existing.Revision != updated.Revision {
		var oldSpec automation.DefinitionSpec
		if err := json.Unmarshal([]byte(existing.SpecJSON), &oldSpec); err != nil {
			rotate = true
		} else if old, oldErr := automation.Effective(oldSpec, existing.Revision); oldErr != nil {
			rotate = true
		} else if newSnapshot, newErr := automation.Effective(spec, updated.Revision); newErr != nil {
			rotate = true
		} else {
			rotate = !old.ContinuationContract().Equal(newSnapshot.ContinuationContract())
		}
	}
	if !rotate {
		return nil
	}
	return d.store.ReleaseAutomationContinuityBindings(spec.ID, store.AutomationBindingReleasedContractRotated, time.Now())
}

func (d *Daemon) cancelPendingAutomationRuns(definitionID, reason string) error {
	pending, err := d.store.ListPendingAutomationRuns()
	if err != nil {
		return err
	}
	message := "automation definition disabled before delivery"
	if reason == store.AutomationCancelReasonDefinitionDeleted {
		message = "automation definition deleted before delivery"
	}
	for i := range pending {
		run := pending[i]
		if run.DefinitionID != definitionID {
			continue
		}
		if _, cancelErr := d.cancelAutomationRun(&run, reason, message); cancelErr != nil {
			err = errors.Join(err, cancelErr)
		}
	}
	return err
}

func (d *Daemon) automationSetEnabled(ctx context.Context, definitionID string, enabled bool) (*store.AutomationDefinition, error) {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("deadline exceeded waiting for an in-flight automation delivery: %w", err)
	}
	definition, changed, err := d.store.SetAutomationEnabled(definitionID, enabled, time.Now())
	if err != nil {
		return nil, err
	}
	if definition == nil {
		return nil, fmt.Errorf("automation %q not found", definitionID)
	}
	if !changed {
		return definition, nil
	}
	if !enabled {
		err = d.cancelPendingAutomationRuns(definitionID, store.AutomationCancelReasonDefinitionDisabled)
	}
	d.broadcastAutomationsChanged(definitionID)
	return definition, err
}

func (d *Daemon) automationDelete(ctx context.Context, definitionID string) error {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deadline exceeded waiting for an in-flight automation delivery: %w", err)
	}
	definition, err := d.store.GetAutomationDefinition(definitionID)
	if err != nil {
		return err
	}
	if definition == nil {
		return fmt.Errorf("automation %q not found", definitionID)
	}
	now := time.Now()
	if err := d.cancelPendingAutomationRuns(definitionID, store.AutomationCancelReasonDefinitionDeleted); err != nil {
		return err
	}
	if err := d.store.DeactivateAutomationReviewRequestEdges(definitionID, now); err != nil {
		return err
	}
	if err := d.store.ReleaseAutomationContinuityBindings(definitionID, store.AutomationBindingReleasedDefinitionDeleted, now); err != nil {
		return err
	}
	if err := d.store.FenceAutomationProviderCursors(definitionID, now); err != nil {
		return err
	}
	if err := d.store.DeleteAutomationDefinition(definitionID, now); err != nil {
		return err
	}
	d.broadcastAutomationsChanged(definitionID)
	return nil
}
