package daemon

import (
	"context"

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
		if err == nil && run.State == store.AutomationRunStatePending && d.recoveryDelivers(run, occurrence) {
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

func (d *Daemon) recoveryDelivers(run *store.AutomationRun, occurrence *store.AutomationOccurrence) bool {
	if occurrence == nil || occurrence.Provider != "github" {
		return true
	}
	req, err := d.automationRunRequest(run)
	return err == nil && d.automationRunAgentStarted(run, req)
}

func recoverAutomationsAfterGitHubReady(ready, stopping <-chan struct{}, recover func()) bool {
	select {
	case <-ready:
	case <-stopping:
		return false
	}
	select {
	case <-stopping:
		return false
	default:
	}
	recover()
	return true
}
