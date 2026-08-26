package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Design: docs/plans/2026-08-16-pi-auto-mode.md.

const automodeDenialsDefaultLimit = 20

func (d *Daemon) sendAutoModeResponse(conn net.Conn, resp protocol.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("automode: writing response: %v", err)
	}
}

func (d *Daemon) requireAutoModeStore(conn net.Conn) bool {
	if d.store == nil {
		d.sendError(conn, "no database")
		return false
	}
	return true
}

func (d *Daemon) handleAutoModeShow(conn net.Conn, _ *protocol.AutoModeShowMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	proposals, err := d.store.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok: true,
		AutomodeShowResult: &protocol.AutoModeShowResult{
			Config:    autoModeConfigInfo(cfg),
			Proposals: autoModeProposalInfos(proposals),
		},
	})
}

func (d *Daemon) handleAutoModeEnvSlot(conn net.Conn, msg *protocol.AutoModeEnvSlotMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	updated, err := d.store.SetAutoModeEnvironmentSlot(msg.Slot, msg.Values, time.Now())
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok:                true,
		AutomodeEnvResult: &protocol.AutoModeEnvResult{Environment: autoModeEnvironmentInfo(updated.Environment)},
	})
}

func (d *Daemon) handleAutoModeEnvNotes(conn net.Conn, msg *protocol.AutoModeEnvNotesMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	updated, err := d.store.SetAutoModeEnvironmentNotes(cleanEnvironmentLines(msg.Notes), time.Now())
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok:                true,
		AutomodeEnvResult: &protocol.AutoModeEnvResult{Environment: autoModeEnvironmentInfo(updated.Environment)},
	})
}

func cleanEnvironmentLines(lines []string) []string {
	cleaned := []string{}
	for _, line := range lines {
		cleaned = append(cleaned, strings.TrimRight(line, " \t"))
	}
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return cleaned
}

// autoModeEnvironmentInfo puts the slots on the wire in the schema's order, so
func autoModeEnvironmentInfo(env automode.Environment) protocol.AutoModeEnvironmentInfo {
	values := []protocol.AutoModeEnvironmentSlotValue{}
	for _, id := range automode.SlotIDs() {
		if entries := env.Slots[id]; len(entries) > 0 {
			values = append(values, protocol.AutoModeEnvironmentSlotValue{
				ID: id, Values: nonNilStrings(entries),
			})
		}
	}
	return protocol.AutoModeEnvironmentInfo{Slots: values, Notes: nonNilStrings(env.Notes)}
}

func autoModeEnvironmentSlots() []protocol.AutoModeEnvironmentSlot {
	slots := automode.Slots()
	infos := make([]protocol.AutoModeEnvironmentSlot, 0, len(slots))
	for _, slot := range slots {
		infos = append(infos, protocol.AutoModeEnvironmentSlot{
			ID:       slot.ID,
			Label:    slot.Label,
			Kind:     slot.Kind,
			Choices:  nonNilStrings(slot.Choices),
			Detail:   slot.Detail,
			Unset:    slot.Unset,
			Detected: slot.Detected,
			ReadBy:   nonNilStrings(slot.ReadBy),
		})
	}
	return infos
}

func (d *Daemon) handleAutoModePropose(conn net.Conn, msg *protocol.AutoModeProposeMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	proposal, err := d.store.CreateAutoModeProposal(
		strings.TrimSpace(msg.Kind),
		strings.TrimSpace(protocol.Deref(msg.Target)),
		strings.TrimSpace(msg.Value),
		strings.TrimSpace(protocol.Deref(msg.ProposedBy)),
		time.Now(),
	)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.publishFact(FactAutoModeConfigChanged, AutoModeConfigSubject, nil)
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok: true,
		AutomodeProposeResult: &protocol.AutoModeProposeResult{
			Proposal: autoModeProposalInfo(proposal),
		},
	})
}

func (d *Daemon) handleAutoModeDenials(conn net.Conn, msg *protocol.AutoModeDenialsMessage) {
	if !d.requireAutoModeStore(conn) {
		return
	}
	limit := automodeDenialsDefaultLimit
	if msg.Limit != nil && *msg.Limit > 0 {
		limit = *msg.Limit
	}
	reconcile := d.reconcileAutoModeDenialLedger()
	denials, err := d.store.ListAutoModeDenials(limit)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendAutoModeResponse(conn, protocol.Response{
		Ok: true,
		AutomodeDenialsResult: &protocol.AutoModeDenialsResult{
			Denials:    autoModeDenialInfos(denials),
			LedgerNote: protocol.Ptr(autoModeLedgerNote(reconcile)),
		},
	})
}

const notificationKindAutoModeDenied = "automode_denied"

func (d *Daemon) recordAutoModeDenial(params pluginReportAutoModeDenialParams) error {
	if d.store == nil {
		return fmt.Errorf("no database")
	}
	sessionID := strings.TrimSpace(params.SessionID)
	at := time.Now()
	if stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(params.At)); err == nil {
		at = stamp
	}
	stored, dropped, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
		SessionID: sessionID,
		Tool:      strings.TrimSpace(params.Tool),
		Signature: strings.TrimSpace(params.Action),
		Reason:    strings.TrimSpace(params.Reason),
		Rule:      strings.TrimSpace(params.Rule),
	}, at)
	if err != nil {
		return err
	}
	if dropped > 0 {
		d.logf("automode: denial log is at its %d-row cap; dropped %d oldest", store.AutoModeDenialRows, dropped)
	}
	notification := ""
	record, err := d.store.AddNotification(autoModeDenialNotification(d.sessionLabel(sessionID), stored), time.Now())
	if err != nil {
		d.logf("automode: add denial notification for session %s: %v", sessionID, err)
	} else {
		notification = record.ID
	}
	d.logf("automode: denied session=%s rule=%s action=%q notification=%s",
		sessionID, stored.Rule, stored.Signature, notification)
	if notification == "" {
		return nil
	}
	d.publishFact(FactAutoModeDenied, sessionID, nil)
	return nil
}

func (d *Daemon) sessionLabel(sessionID string) string {
	if session := d.store.Get(sessionID); session != nil && strings.TrimSpace(session.Label) != "" {
		return session.Label
	}
	return sessionID
}

func autoModeDenialNotification(label string, denial store.AutoModeDenial) store.NotificationRecord {
	return store.NotificationRecord{
		Kind:       notificationKindAutoModeDenied,
		Severity:   store.NotificationInfo,
		Title:      fmt.Sprintf("Auto mode blocked a call in %s", label),
		Body:       denial.Signature,
		Detail:     fmt.Sprintf("%s (%s)", denial.Reason, denial.Rule),
		SourceKind: "session",
		SourceID:   denial.SessionID,
	}
}

func autoModeConfigInfo(cfg automode.Config) protocol.AutoModeConfigInfo {
	return protocol.AutoModeConfigInfo{
		EnabledDefault:  cfg.EnabledDefault,
		Environment:     autoModeEnvironmentInfo(cfg.Environment),
		Allow:           nonNilStrings(cfg.Allow),
		HardDeny:        nonNilStrings(cfg.HardDeny),
		ShippedHardDeny: nonNilStrings(automode.ShippedHardDeny(config.WSPort())),
		Models:          nonNilStrings(cfg.Models),
	}
}

func autoModeProposalInfo(p store.AutoModeProposal) protocol.AutoModeProposalInfo {
	return protocol.AutoModeProposalInfo{
		ID:         int(p.ID),
		Kind:       p.Kind,
		Target:     p.Target,
		Value:      p.Value,
		ProposedBy: p.ProposedBy,
		State:      p.State,
		CreatedAt:  formatAutoModeStamp(p.CreatedAt),
		ResolvedAt: formatAutoModeStamp(p.ResolvedAt),
	}
}

func autoModeProposalInfos(proposals []store.AutoModeProposal) []protocol.AutoModeProposalInfo {
	out := make([]protocol.AutoModeProposalInfo, 0, len(proposals))
	for _, p := range proposals {
		out = append(out, autoModeProposalInfo(p))
	}
	return out
}

func autoModeDenialInfos(denials []store.AutoModeDenial) []protocol.AutoModeDenialInfo {
	out := make([]protocol.AutoModeDenialInfo, 0, len(denials))
	for _, denial := range denials {
		out = append(out, protocol.AutoModeDenialInfo{
			ID:        int(denial.ID),
			SessionID: denial.SessionID,
			Tool:      denial.Tool,
			Signature: denial.Signature,
			Reason:    denial.Reason,
			Rule:      denial.Rule,
			CreatedAt: formatAutoModeStamp(denial.CreatedAt),
		})
	}
	return out
}

func formatAutoModeStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
