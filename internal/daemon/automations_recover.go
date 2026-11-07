package daemon

import (
	"context"
	"encoding/json"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) recoverAutomations() {
	runs, err := d.store.ListPendingAutomationRuns()
	if err != nil {
		d.logf("automation recovery list: %v", err)
		return
	}
	for i := range runs {
		if d.stopping() {
			return
		}
		occurrence, occurrenceErr := d.store.GetAutomationOccurrence(runs[i].OccurrenceID)
		if occurrenceErr != nil {
			d.logf("automation recovery occurrence %s: %v", runs[i].OccurrenceID, occurrenceErr)
			continue
		}
		d.automationMu.Lock()
		run, err := d.store.GetAutomationRun(runs[i].ID)
		if err == nil && run.State == store.AutomationRunStatePending && automationRunRecovers(run, occurrence) {
			err = d.deliverAutomationRun(context.Background(), run)
			if err != nil {
				_, err = d.handleAutomationDeliveryError(run, err)
			}
		}
		d.automationMu.Unlock()
		if err != nil {
			d.logf("automation recovery run %s: %v", runs[i].ID, err)
		}
	}
}

func automationRunRecovers(run *store.AutomationRun, occurrence *store.AutomationOccurrence) bool {
	return run.LaunchedAt != nil || occurrence == nil || occurrence.Provider != "github"
}

func (d *Daemon) recoverAutomationsAtStart() bool {
	if d.pendingRunNeedsGitHub() {
		pausepoint.At(pausepoint.AutomationRecoveryAwaitsGitHub)
		select {
		case <-d.gitHubHostsKnown:
		case <-d.life.Done():
			return false
		}
	}
	if d.stopping() {
		return false
	}
	d.recoverAutomations()
	return true
}

func (d *Daemon) pendingRunNeedsGitHub() bool {
	runs, err := d.store.ListPendingAutomationRuns()
	if err != nil {
		d.logf("automation recovery list: %v", err)
		return false
	}
	for i := range runs {
		occurrence, err := d.store.GetAutomationOccurrence(runs[i].OccurrenceID)
		if err != nil {
			d.logf("automation recovery occurrence %s: %v", runs[i].OccurrenceID, err)
			continue
		}
		var snapshot automation.Snapshot
		if err := json.Unmarshal([]byte(runs[i].SnapshotJSON), &snapshot); err != nil {
			continue
		}
		if automationRunRecovers(&runs[i], occurrence) && snapshot.Location.Type == "repository_worktree" {
			return true
		}
	}
	return false
}
