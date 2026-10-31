package daemon

import (
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleCrewSleep(conn net.Conn, msg *protocol.CrewSleepMessage) {
	result, err := d.crewSleep(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		d.sendCrewError(conn, "sleep", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewSleepResult: result})
}

func (d *Daemon) handleCrewSleepWS(client *wsClient, msg *protocol.CrewSleepMessage) {
	result, err := d.crewSleep(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	response := protocol.CrewSleepResultMessage{
		Event:     protocol.EventCrewSleepResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member = protocol.Ptr(result.Member)
		response.SessionID = result.SessionID
		response.AlreadyAsleep = protocol.Ptr(result.AlreadyAsleep)
		response.DeliveryStatus = result.DeliveryStatus
		response.Detail = protocol.Ptr(result.Detail)
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewSleep(asker crewAsker, name string) (*protocol.CrewSleepResult, error) {
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()

	member, err := d.crewAskedMember(asker, name)
	if err != nil {
		return nil, err
	}
	sessionID := protocol.TrimID(member.BindingSession)
	if sessionID == "" {
		return &protocol.CrewSleepResult{
			Member:        member.ID,
			Name:          member.Name,
			AlreadyAsleep: true,
			Detail:        fmt.Sprintf("%s is already asleep; no sleep request was sent", member.Name),
		}, nil
	}
	live, err := d.crewSessionActuallyLive(sessionID)
	if err != nil {
		return nil, fmt.Errorf("check %s's bound session %s: %w", member.Name, shortSessionID(sessionID), err)
	}
	if !live {
		if _, err := d.releaseCrewBinding(member.ID, sessionID); err != nil {
			return nil, fmt.Errorf("release %s's exited session %s: %w", member.Name, shortSessionID(sessionID), err)
		}
		d.noteCrewExitedSession(member.ID, sessionID)
		return &protocol.CrewSleepResult{
			Member:        member.ID,
			Name:          member.Name,
			AlreadyAsleep: true,
			Detail: fmt.Sprintf("%s is already asleep; previous session %s had exited and its binding was released; no sleep request was sent",
				member.Name, shortSessionID(sessionID)),
		}, nil
	}

	prompt := prompts.RenderText("crew", "sleep-requested", prompts.Values{})
	receipt, err := d.sendToInbox(inbox.Item{ID: uuid.NewString(), To: who.ToSession(sessionID), Kind: inbox.Notice, Text: prompt})
	if err != nil {
		return nil, fmt.Errorf("record %s's sleep request: %w", member.Name, err)
	}
	status := protocol.AgentMsgStatusNotified
	detail := fmt.Sprintf("asked %s in session %s to write its handoff and file it with `attn handoff`", member.Name, shortSessionID(sessionID))
	if !receipt.Rang {
		status = protocol.AgentMsgStatusQueued
		detail = receipt.Detail
	}
	return &protocol.CrewSleepResult{
		Member:         member.ID,
		Name:           member.Name,
		SessionID:      protocol.Ptr(sessionID),
		DeliveryStatus: protocol.Ptr(status),
		Detail:         detail,
	}, nil
}
