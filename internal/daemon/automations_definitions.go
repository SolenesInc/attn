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
	automationErrCodeDeletedElsewhere = "deleted_elsewhere"
	automationErrCodeIDMismatch       = "id_mismatch"
	automationErrCodeValidation       = "validation"
)

const wsAutomationMutationTimeout = 25 * time.Second

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
		if _, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskAutomation, Lane: gitInteractive}, func(ctx context.Context, client *attngit.Client) (string, error) {
			return client.ValidateLocalClone(ctx, source.Path, identity)
		}); err != nil {
			return spec, nil, fmt.Errorf("repository override %s: %w", identity, err)
		}
	}
	return spec, canonical, nil
}

func (d *Daemon) automationApply(raw string) (*store.AutomationDefinition, error) {
	return d.automationApplyWithGuards(context.Background(), raw, "", "", nil, nil, nil)
}

func (d *Daemon) automationApplyWithGuards(ctx context.Context, raw, profileID, scope string, expectedID *int, expectedRevision *int, launch *launchDesktopWrite) (*store.AutomationDefinition, error) {
	if err := d.requireHome(automation.Surface); err != nil {
		return nil, err
	}
	spec, canonical, err := d.validateAutomationSpec(raw)
	if err != nil {
		return nil, &automationRefusal{Code: automationErrCodeValidation, Err: err}
	}
	if expectedID != nil && *expectedID != 0 && spec.ID != *expectedID {
		return nil, &automationRefusal{Code: automationErrCodeIDMismatch, Err: fmt.Errorf("definition id %d in the YAML does not match the definition being edited (%d) — apply is keyed on the id inside the YAML, so creation must omit the id", spec.ID, *expectedID)}
	}
	guard := func(existing *store.AutomationDefinition) error {
		if scope != "" && existing != nil && existing.ProfileID != scope {
			return fmt.Errorf("automation %d does not exist or was deleted; create a new automation without an id", spec.ID)
		}
		if expectedRevision == nil {
			return nil
		}
		if *expectedRevision == 0 {
			if spec.ID != 0 {
				return &automationRefusal{Code: automationErrCodeValidation, Err: errors.New("create an automation without an id; numeric ids are assigned automatically")}
			}
			return nil
		}
		if existing == nil || existing.Revision != *expectedRevision {
			return &automationRefusal{Code: automationErrCodeRevisionConflict, Err: errors.New("automation definition changed elsewhere — reload before saving")}
		}
		if existing.DeletedAt != nil {
			return &automationRefusal{Code: automationErrCodeDeletedElsewhere, Err: fmt.Errorf("automation %d was deleted elsewhere while you were editing it — your changes were not saved; close this editor and create a new automation", spec.ID)}
		}
		return nil
	}
	if spec.ID == 0 {
		profile, err := d.requestedOrRecentProfile(profileID)
		if err != nil {
			return nil, &automationRefusal{Code: automationErrCodeValidation, Err: err}
		}
		profileID = profile.ID
	}
	return d.automationApplyLocked(ctx, spec, canonical, profileID, guard, launch)
}

func (d *Daemon) automationApplyLocked(ctx context.Context, spec automation.DefinitionSpec, canonical []byte, profileID string, guard func(*store.AutomationDefinition) error, launch *launchDesktopWrite) (*store.AutomationDefinition, error) {
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
	var setting *store.LaunchDesktopSetting
	if launch != nil {
		setting = storeLaunchSetting(launch.setting)
	}
	if launch != nil && launch.ref != nil {
		owner := profileID
		if existing != nil {
			owner = existing.ProfileID
		}
		chosen, err := d.launchDesktopFromRef(owner, spec.Name, *launch.ref, launch.name)
		if err != nil {
			return nil, err
		}
		setting = &chosen
	}
	definition, err := d.store.UpsertAutomationDefinitionWithLaunch(spec.ID, spec.Name, string(canonical), profileID, time.Now(), setting)
	if err != nil {
		return definition, err
	}
	if err := d.rotateContinuityBindingsIfContractChanged(existing, spec, definition); err != nil {
		return definition, err
	}
	if setting != nil || existing == nil {
		d.publishArrangementChanged(definition.ProfileID)
	}
	d.broadcastAutomationsChanged(definition.ID)
	if definition.Enabled {
		return definition, nil
	}
	return definition, d.cancelPendingAutomationRuns(definition.ID, store.AutomationCancelReasonDefinitionDisabled)
}

func (d *Daemon) rotateContinuityBindingsIfContractChanged(existing *store.AutomationDefinition, spec automation.DefinitionSpec, updated *store.AutomationDefinition) error {
	if existing == nil {
		return nil
	}
	rotate := false
	if existing.Revision != updated.Revision {
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

func (d *Daemon) cancelPendingAutomationRuns(definitionID int, reason string) error {
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

func (d *Daemon) automationSetEnabled(ctx context.Context, definitionID int, enabled bool) (*store.AutomationDefinition, error) {
	if enabled {
		if err := d.requireHome(automation.Surface); err != nil {
			return nil, err
		}
	}
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
		return nil, fmt.Errorf("automation %d not found", definitionID)
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

func (d *Daemon) automationDelete(ctx context.Context, definitionID int) error {
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
		return fmt.Errorf("automation %d not found", definitionID)
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
