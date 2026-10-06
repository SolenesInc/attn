package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func automationProvenance(record store.AutomationProvenanceRecord) (*protocol.AutomationProvenance, error) {
	provenance := &protocol.AutomationProvenance{
		RunID:          record.RunID,
		DefinitionID:   record.DefinitionID,
		DefinitionName: record.DefinitionName,
		TriggerType:    "unknown",
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(record.DefinitionSpecJSON), &spec); err != nil {
		return provenance, fmt.Errorf("parse automation definition %d provenance: %w", record.DefinitionID, err)
	}
	provenance.TriggerType = spec.Trigger.Type
	if record.Provider != "github" {
		return provenance, nil
	}
	input, err := automation.ParsePullRequestInput(json.RawMessage(record.PayloadJSON))
	if err != nil {
		return provenance, fmt.Errorf("parse automation run %s pull request provenance: %w", record.RunID, err)
	}
	pr := &protocol.PullRequestProvenance{
		Repository: input.RepositoryIdentity(),
		Number:     input.Number,
		URL:        input.URL,
		HeadSHA:    input.HeadSHA,
	}
	if title := strings.TrimSpace(input.Title); title != "" {
		pr.Title = protocol.Ptr(title)
	}
	provenance.PullRequest = pr
	return provenance, nil
}

func (d *Daemon) latestAutomationProvenance() map[protocol.SessionID]*protocol.AutomationProvenance {
	bySession := make(map[protocol.SessionID]*protocol.AutomationProvenance)
	records, err := d.store.ListLatestAutomationProvenanceRecords()
	if err != nil {
		d.logf("list automation provenance: %v", err)
		return bySession
	}
	for _, record := range records {
		if record.SessionID == "" || bySession[record.SessionID] != nil {
			continue
		}
		provenance, err := automationProvenance(record)
		if err != nil {
			d.logf("automation provenance: %v", err)
		}
		bySession[record.SessionID] = provenance
	}
	return bySession
}

func (d *Daemon) automationProvenanceForSession(sessionID protocol.SessionID) *protocol.AutomationProvenance {
	record, err := d.store.GetLatestAutomationProvenanceRecordForSession(sessionID)
	return d.automationProvenanceFromRecord("session", sessionID, record, err)
}

func (d *Daemon) automationProvenanceFromRecord(kind string, id protocol.SessionID, record *store.AutomationProvenanceRecord, err error) *protocol.AutomationProvenance {
	if err != nil {
		d.logf("load automation provenance for %s %s: %v", kind, id, err)
		return nil
	}
	if record == nil {
		return nil
	}
	provenance, buildErr := automationProvenance(*record)
	if buildErr != nil {
		d.logf("automation provenance for %s %s: %v", kind, id, buildErr)
	}
	return provenance
}

func automationReviewNames(req automation.WorkRequest) (pullRequest, session, seedTitle string, ok bool) {
	input, err := automation.ParsePullRequestInput(req.Context)
	if err != nil {
		return "", "", "", false
	}
	pullRequest = fmt.Sprintf("%s#%d", input.Repository, input.Number)
	session = pullRequest
	if model := strings.TrimSpace(req.Launch.Model); model != "" {
		session += " · " + model
	}
	return pullRequest, session, "Review " + session, true
}
