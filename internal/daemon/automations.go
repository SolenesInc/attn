package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) automationRun(ctx context.Context, definitionID int, requestID, input string, wait bool) (*store.AutomationRun, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	if input == "" {
		input = "{}"
	}
	if !json.Valid([]byte(input)) {
		return nil, fmt.Errorf("input_json must be valid JSON")
	}
	release, held := d.life.Hold("automationRun")
	if !held {
		return nil, errDaemonStopping
	}
	defer release()
	def, err := d.store.GetAutomationDefinition(definitionID)
	if err != nil || def == nil {
		if err == nil {
			err = fmt.Errorf("automation %d not found", definitionID)
		}
		return nil, err
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		return nil, err
	}
	snapshot, err := automation.Effective(spec, def.Revision)
	if err != nil {
		return nil, err
	}
	snapshot = snapshot.OnDemand()
	subjectKey := ""
	canonicalInput := input
	if spec.Location.Type == "repository_worktree" {
		if input == "{}" {
			return nil, protocol.Refuse(protocol.RefusalInvalidInput, "automation %d works on a pull request; give it one: {attn automation run %d --pr-url <url>}", definitionID, definitionID)
		}
		pr, err := automation.ParsePullRequestInput(json.RawMessage(input))
		if err != nil {
			return nil, err
		}
		canonical, err := json.Marshal(pr)
		if err != nil {
			return nil, err
		}
		canonicalInput = string(canonical)
		subjectKey = pr.SubjectKey()
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	ids, err := d.newAutomationRunReservation(def)
	if err != nil {
		return nil, err
	}
	run, created, err := d.store.ClaimManualAutomationRun(definitionID, requestID, subjectKey, canonicalInput, def.Revision, string(snapshotJSON), time.Now(), ids)
	if err != nil {
		return nil, err
	}
	pausepoint.At(pausepoint.AutomationRunReserved)
	if wait {
		return d.deliverClaimedAutomationRun(ctx, definitionID, run.ID, created)
	}
	if created && run.State == store.AutomationRunStateFailed {
		return nil, errors.New(run.LastError)
	}
	claimed := run.ID
	if !d.life.Go("automationRunDelivery", func() {
		if _, err := d.deliverClaimedAutomationRun(d.life.Context(), definitionID, claimed, created); err != nil {
			d.logf("automation run %s of %d: %v", claimed, definitionID, err)
		}
	}) {
		return nil, errDaemonStopping
	}
	return run, nil
}

func (d *Daemon) deliverClaimedAutomationRun(ctx context.Context, definitionID int, runID string, created bool) (*store.AutomationRun, error) {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	run, err := d.store.GetAutomationRun(runID)
	if err != nil {
		return nil, err
	}
	d.broadcastAutomationsChanged(definitionID)
	if created && run.State == store.AutomationRunStateFailed {
		return nil, errors.New(run.LastError)
	}
	if run.State != store.AutomationRunStatePending {
		return run, nil
	}
	if err := d.deliverAutomationRun(ctx, run); err != nil {
		return d.handleAutomationDeliveryError(run, err)
	}
	return d.store.GetAutomationRun(run.ID)
}
func (d *Daemon) newAutomationRunReservation(definition *store.AutomationDefinition) (store.AutomationRunReservation, error) {
	runID := uuid.NewString()
	seedID, err := d.mintAutomationSeedID()
	if err != nil {
		return store.AutomationRunReservation{}, err
	}
	return store.AutomationRunReservation{RunID: runID, OccurrenceID: uuid.NewString(), SeedID: seedID, SessionID: protocol.SessionID(uuid.NewString())}, nil
}

func (d *Daemon) mintAutomationSeedID() (string, error) {
	schema, found, err := d.store.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		return "", err
	}
	if found {
		return d.mintUnplantedSeedID(*schema)
	}
	return d.mintSeedID()
}
