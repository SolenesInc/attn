package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
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

	member, doc, err := d.crewMember(identity.Key)
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
		if restart, pending := pendingCrewRestartFor(member, sessionID); pending {
			if err := d.failCrewRestart(member.Key, restart.RequestID, sessionID, "", fmt.Errorf("session %s exited before the restart ran and %s was put to sleep", shortSessionID(sessionID), d.storedMemberName(member.Key.String()))); err != nil {
				return nil, err
			}
		}
		return &protocol.CrewSleepResult{
			Member:        member.Key.String(),
			Name:          d.memberName(member.Key),
			AlreadyAsleep: true,
			Detail: fmt.Sprintf("%s is already asleep; previous session %s had exited and its binding was released; no sleep request was sent",
				d.storedMemberName(member.Key.String()), shortSessionID(sessionID)),
		}, nil
	}

	now := time.Now()
	deliveryID := uuid.NewString()
	item := inbox.Item{ID: deliveryID, To: who.ToSession(sessionID), Kind: inbox.Notice, Text: crewRequestedSleepPrompt}
	var receipt inbox.Receipt
	if _, pending := pendingCrewRestartFor(member, sessionID); pending {
		member.Restart.State = crew.RestartFailed
		member.Restart.Withdrawn = true
		member.Restart.Error = fmt.Sprintf("the restart was withdrawn because the user asked %s to sleep instead", d.storedMemberName(member.Key.String()))
		schema, schemaErr := d.crewCollection()
		if schemaErr != nil {
			return nil, schemaErr
		}
		body, encodeErr := member.Encode()
		if encodeErr != nil {
			return nil, encodeErr
		}
		fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.Key.String(), false)
		written, commitErr := d.store.CommitDocumentWriteWithInbox(
			store.DocumentWrite{Schema: *schema, ID: member.Key.String(), Body: body, Expected: &doc.Rev},
			fact, item, now,
		)
		if commitErr != nil {
			return nil, fmt.Errorf("record %s's sleep request: %w", d.storedMemberName(member.Key.String()), commitErr)
		}
		d.announceCommittedWrite(fact, written.Seq)
		d.publishFact(FactCrewUpdated, member.Key.String(), nil)
		receipt = d.deliverSavedInbox(item.To, deliveryID, now)
	} else {
		receipt, err = d.sendToInbox(item)
	}
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
