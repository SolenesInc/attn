package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) automationsRunHere(activity string) bool {
	if err := d.requireHome(automation.Surface); err != nil {
		d.logf("automations: skipping %s on this daemon: %v", activity, err)
		return false
	}
	return true
}

func (d *Daemon) automationRun(ctx context.Context, definitionID, requestID, input string) (*store.AutomationRun, error) {
	if err := d.requireHome(automation.Surface); err != nil {
		return nil, err
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	if input == "" {
		input = "{}"
	}
	if !json.Valid([]byte(input)) {
		return nil, fmt.Errorf("input_json must be valid JSON")
	}
	def, err := d.store.GetAutomationDefinition(definitionID)
	if err != nil || def == nil {
		if err == nil {
			err = fmt.Errorf("automation %q not found", definitionID)
		}
		return nil, err
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		return nil, err
	}
	if spec.Trigger.Type != "manual" {
		return nil, fmt.Errorf("automation %q is provider-driven and cannot be run manually yet", definitionID)
	}
	snapshot, err := automation.Effective(spec, def.Revision)
	if err != nil {
		return nil, err
	}
	subjectKey := ""
	canonicalInput := input
	if spec.Location.Type == "repository_worktree" {
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
	run, _, err := d.store.ClaimManualAutomationRun(definitionID, requestID, subjectKey, canonicalInput, def.Revision, string(snapshotJSON), time.Now(), ids)
	if err != nil {
		return nil, err
	}
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	run, err = d.store.GetAutomationRun(run.ID)
	if err != nil {
		return nil, err
	}
	d.broadcastAutomationsChanged(definitionID)
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
	return store.AutomationRunReservation{RunID: runID, OccurrenceID: uuid.NewString(), SeedID: seedID, SessionID: uuid.NewString()}, nil
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
func (d *Daemon) automationObservationLock(definitionID, subjectKey string, cycle int) *sync.Mutex {
	key := fmt.Sprintf("%s\x00%s\x00%d", definitionID, subjectKey, cycle)
	d.automationObservationMu.Lock()
	defer d.automationObservationMu.Unlock()
	if d.automationObservationLocks == nil {
		d.automationObservationLocks = make(map[string]*sync.Mutex)
	}
	lock := d.automationObservationLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		d.automationObservationLocks[key] = lock
	}
	return lock
}

func (d *Daemon) handleAutomationCommand(conn net.Conn, cmd string, msg any) {
	ctx := context.Background()
	var result any
	switch cmd {
	case protocol.CmdAutomationApply:
		result = d.actionAutomationApply(ctx, msg.(*protocol.AutomationApplyMessage))
	case protocol.CmdAutomationValidate:
		result = d.actionAutomationValidate(msg.(*protocol.AutomationValidateMessage))
	case protocol.CmdAutomationDefinitionsGet:
		result = d.actionAutomationDefinitionsGet(msg.(*protocol.AutomationDefinitionsGetMessage))
	case protocol.CmdAutomationDefinitionGet:
		result = d.actionAutomationDefinitionGet(msg.(*protocol.AutomationDefinitionGetMessage))
	case protocol.CmdAutomationRun:
		result = d.actionAutomationRun(ctx, msg.(*protocol.AutomationRunMessage))
	case protocol.CmdAutomationRunsGet:
		result = d.actionAutomationRunsGet(msg.(*protocol.AutomationRunsGetMessage))
	case protocol.CmdAutomationSetEnabled:
		result = d.actionAutomationSetEnabled(ctx, msg.(*protocol.AutomationSetEnabledMessage))
	case protocol.CmdAutomationDelete:
		result = d.actionAutomationDelete(ctx, msg.(*protocol.AutomationDeleteMessage))
	case protocol.CmdAutomationCleanup:
		result = d.actionAutomationCleanup(ctx, msg.(*protocol.AutomationCleanupMessage))
	}
	_ = json.NewEncoder(conn).Encode(result)
}
