package daemon

import (
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleCrewApply(conn net.Conn, msg *protocol.CrewApplyMessage) {
	result, err := d.crewApply(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		d.sendCrewError(conn, "apply", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewApplyResult: result})
}

func (d *Daemon) handleCrewApplyWS(client *wsClient, msg *protocol.CrewApplyMessage) {
	result, err := d.crewApply(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	response := protocol.CrewApplyResultMessage{
		Event:     protocol.EventCrewApplyResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewApply(asker crewAsker, name string) (*protocol.CrewApplyResult, error) {
	member, session, result, err := d.crewApplyTarget(asker, name)
	if err != nil || result != nil {
		return result, err
	}
	if strings.EqualFold(strings.TrimSpace(session.Agent), member.LaunchAgent()) {
		return d.crewApplyInPlace(member, session.ID)
	}
	return d.crewRequestHarnessSwitch(member, session.ID)
}

func (d *Daemon) crewApplyTarget(asker crewAsker, name string) (crew.Member, *protocol.Session, *protocol.CrewApplyResult, error) {
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	member, err := d.crewAskedMember(asker, name)
	if err != nil {
		return crew.Member{}, nil, nil, err
	}
	sessionID, asleep, err := d.crewAwakeSession(member)
	if err != nil {
		return crew.Member{}, nil, nil, err
	}
	session := d.store.Get(sessionID)
	if asleep != "" || session == nil {
		return member, nil, &protocol.CrewApplyResult{
			Member:  member.ID,
			Name:    member.Name,
			Outcome: protocol.CrewApplyOutcomeAsleep,
			Detail:  asleep + "; its next wake uses the saved settings",
		}, nil
	}
	if err := d.mayActOnSession(asker.session, session, "apply settings to"); err != nil {
		return crew.Member{}, nil, nil, err
	}
	return member, session, nil, nil
}

func (d *Daemon) crewApplyInPlace(member crew.Member, sessionID protocol.SessionID) (*protocol.CrewApplyResult, error) {
	d.clearCrewSwitching(member.ID)
	if err := d.reloadSessionForClient(sessionID, 80, 24); err != nil {
		return nil, fmt.Errorf("reload %s's session %s: %w", member.Name, shortSessionID(sessionID), err)
	}
	prompt := prompts.RenderText("crew", "settings-applied", prompts.Values{"charter_path": member.CharterPath(d.dataRoot)})
	if _, err := d.sendToInbox(inbox.Item{ID: uuid.NewString(), To: who.ToSession(sessionID), Kind: inbox.Notice, Text: prompt}); err != nil {
		return nil, fmt.Errorf("%s's session %s reloaded, but asking it to reread its charter failed: %w", member.Name, shortSessionID(sessionID), err)
	}
	return &protocol.CrewApplyResult{
		Member:    member.ID,
		Name:      member.Name,
		Outcome:   protocol.CrewApplyOutcomeReloaded,
		SessionID: protocol.Ptr(sessionID),
		Detail:    fmt.Sprintf("Reloaded %s's session %s in place on %s with its saved settings; its conversation continues", member.Name, shortSessionID(sessionID), member.LaunchAgent()),
	}, nil
}

func (d *Daemon) crewRequestHarnessSwitch(member crew.Member, sessionID protocol.SessionID) (*protocol.CrewApplyResult, error) {
	if err := d.markCrewSwitching(member, sessionID); err != nil {
		return nil, err
	}
	prompt := prompts.RenderText("crew", "switch-requested", prompts.Values{"agent": member.LaunchAgent()})
	receipt, err := d.sendToInbox(inbox.Item{ID: uuid.NewString(), To: who.ToSession(sessionID), Kind: inbox.Notice, Text: prompt})
	if err != nil {
		return nil, fmt.Errorf("ask %s to file its letter: %w", member.Name, err)
	}
	status := protocol.AgentMsgStatusNotified
	detail := fmt.Sprintf("Asked %s in session %s to file its letter; its next session starts on %s from it", member.Name, shortSessionID(sessionID), member.LaunchAgent())
	if !receipt.Rang {
		status = protocol.AgentMsgStatusQueued
		detail = receipt.Detail
	}
	return &protocol.CrewApplyResult{
		Member:         member.ID,
		Name:           member.Name,
		Outcome:        protocol.CrewApplyOutcomeLetterRequested,
		SessionID:      protocol.Ptr(sessionID),
		DeliveryStatus: protocol.Ptr(status),
		Detail:         detail,
	}, nil
}

func (d *Daemon) markCrewSwitching(member crew.Member, sessionID protocol.SessionID) error {
	marked, err := d.updateCrewMember(member.ID, func(member *crew.Member) (bool, error) {
		if member.BindingSession != sessionID {
			return false, nil
		}
		member.SwitchingSession = sessionID
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("record %s's harness switch: %w", member.Name, err)
	}
	if marked.SwitchingSession != sessionID {
		return fmt.Errorf("%s's session changed from %s while its settings were applied; nothing was asked", member.Name, shortSessionID(sessionID))
	}
	return nil
}

func (d *Daemon) clearCrewSwitching(memberID string) {
	if _, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		if member.SwitchingSession == "" {
			return false, nil
		}
		member.SwitchingSession = ""
		return true, nil
	}); err != nil {
		d.logf("crew: clearing %s's harness switch: %v", memberID, err)
	}
}
