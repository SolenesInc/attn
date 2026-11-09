package daemon

import (
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleCrewClose(conn net.Conn, msg *protocol.CrewCloseMessage) {
	result, err := d.crewClose(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		d.sendCrewError(conn, "close", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewCloseResult: result})
}

func (d *Daemon) handleCrewCloseWS(client *wsClient, msg *protocol.CrewCloseMessage) {
	result, err := d.crewClose(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	response := protocol.CrewCloseResultMessage{
		Event:     protocol.EventCrewCloseResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member = protocol.Ptr(result.Member)
		response.SessionID = result.SessionID
		response.AlreadyClosed = protocol.Ptr(result.AlreadyClosed)
		response.DeliveryStatus = result.DeliveryStatus
		response.Detail = protocol.Ptr(result.Detail)
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewClose(asker crewAsker, name string) (*protocol.CrewCloseResult, error) {
	d.crewStartMu.Lock()
	defer d.crewStartMu.Unlock()

	member, err := d.crewAskedMember(asker, name)
	if err != nil {
		return nil, err
	}
	sessionID, between, err := d.crewLiveSession(member)
	if err != nil {
		return nil, err
	}
	if between != "" {
		return &protocol.CrewCloseResult{
			Member:        member.ID,
			Name:          member.Name,
			AlreadyClosed: true,
			Detail:        between + "; no close request was sent",
		}, nil
	}

	d.clearCrewSwitching(member.ID)
	prompt := prompts.RenderText("crew", "close-requested", prompts.Values{})
	receipt, err := d.sendToInbox(inbox.Item{ID: uuid.NewString(), To: who.ToSession(sessionID), Kind: inbox.Notice, Text: prompt})
	if err != nil {
		return nil, fmt.Errorf("record %s's close request: %w", member.Name, err)
	}
	status := protocol.AgentMsgStatusNotified
	detail := fmt.Sprintf("asked %s in session %s to write its handoff and file it with `attn handoff`", member.Name, shortSessionID(sessionID))
	if !receipt.Rang {
		status = protocol.AgentMsgStatusQueued
		detail = receipt.Detail
	}
	return &protocol.CrewCloseResult{
		Member:         member.ID,
		Name:           member.Name,
		SessionID:      protocol.Ptr(sessionID),
		DeliveryStatus: protocol.Ptr(status),
		Detail:         detail,
	}, nil
}

func (d *Daemon) crewLiveSession(member crew.Member) (sessionID protocol.SessionID, between string, err error) {
	sessionID = protocol.TrimID(member.BindingSession)
	if sessionID == "" {
		return "", fmt.Sprintf("%s is between sessions", member.Name), nil
	}
	live, err := d.crewSessionActuallyLive(sessionID)
	if err != nil {
		return "", "", fmt.Errorf("check %s's bound session %s: %w", member.Name, shortSessionID(sessionID), err)
	}
	if live {
		return sessionID, "", nil
	}
	if _, err := d.releaseCrewBinding(member.ID, sessionID); err != nil {
		return "", "", fmt.Errorf("release %s's exited session %s: %w", member.Name, shortSessionID(sessionID), err)
	}
	d.noteCrewExitedSession(member.ID, sessionID)
	return "", fmt.Sprintf("%s is between sessions; previous session %s had exited and its binding was released", member.Name, shortSessionID(sessionID)), nil
}
