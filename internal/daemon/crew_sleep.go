package daemon

import (
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

var crewRequestedSleepPrompt = prompts.RenderText("crew", "sleep-requested", prompts.Values{})

func (d *Daemon) handleCrewSleep(conn net.Conn, msg *protocol.CrewSleepMessage) {
	b, bindingsErr := d.bindings()
	if bindingsErr != nil {
		d.sendError(conn, bindingsErr.Error())
		return
	}
	r, scopeErr := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, b)
	if scopeErr != nil {
		d.sendCrewError(conn, "request", scopeErr)
		return
	}

	result, err := d.crewSleep(r, strings.TrimSpace(msg.Member))
	if err != nil {
		d.sendCrewError(conn, "sleep", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewSleepResult: result})
}

func (d *Daemon) handleCrewSleepWS(client *wsClient, msg *protocol.CrewSleepMessage) {
	r := who.RequestFromApp(client.selectedProfile())

	result, err := d.crewSleep(r, strings.TrimSpace(msg.Member))
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

func (d *Daemon) crewSleep(r who.Requester, name string) (*protocol.CrewSleepResult, error) {
	identity, err := d.resolveMember(r, name)
	if err != nil {
		return nil, err
	}

	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()

	member, _, err := d.crewMember(identity.Key)
	if err != nil {
		return nil, err
	}
	sessionID := protocol.TrimID(member.BindingSession)
	if sessionID == "" {
		return &protocol.CrewSleepResult{
			Member:        member.Key.String(),
			Name:          d.memberName(member.Key),
			AlreadyAsleep: true,
			Detail:        fmt.Sprintf("%s is already asleep; no sleep request was sent", d.storedMemberName(member.Key.String())),
		}, nil
	}
	live, err := d.crewSessionActuallyLive(sessionID)
	if err != nil {
		return nil, fmt.Errorf("check %s's bound session %s: %w", d.storedMemberName(member.Key.String()), shortSessionID(sessionID), err)
	}
	if !live {
		if _, err := d.releaseCrewBinding(member.Key, sessionID); err != nil {
			return nil, fmt.Errorf("release %s's exited session %s: %w", d.storedMemberName(member.Key.String()), shortSessionID(sessionID), err)
		}
		d.noteCrewExitedSession(member.Key.String(), sessionID)
		return &protocol.CrewSleepResult{
			Member:        member.Key.String(),
			Name:          d.memberName(member.Key),
			AlreadyAsleep: true,
			Detail: fmt.Sprintf("%s is already asleep; previous session %s had exited and its binding was released; no sleep request was sent",
				d.storedMemberName(member.Key.String()), shortSessionID(sessionID)),
		}, nil
	}

	receipt, err := d.sendToInbox(inbox.Item{ID: uuid.NewString(), To: who.ToSession(sessionID), Kind: inbox.Notice, Text: crewRequestedSleepPrompt})
	if err != nil {
		return nil, fmt.Errorf("record %s's sleep request: %w", d.storedMemberName(member.Key.String()), err)
	}
	status := protocol.AgentMsgStatusNotified
	detail := fmt.Sprintf("asked %s in session %s to write its handoff and file it with `attn handoff --sleep`", d.storedMemberName(member.Key.String()), shortSessionID(sessionID))
	if !receipt.Rang {
		status = protocol.AgentMsgStatusQueued
		detail = receipt.Detail
	}
	return &protocol.CrewSleepResult{
		Member:         member.Key.String(),
		Name:           d.memberName(member.Key),
		SessionID:      protocol.Ptr(sessionID),
		DeliveryStatus: protocol.Ptr(status),
		Detail:         detail,
	}, nil
}
