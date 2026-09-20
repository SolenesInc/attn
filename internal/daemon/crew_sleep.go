package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var crewRequestedSleepPrompt = prompts.RenderText("crew", "sleep-requested", prompts.Values{})

func (d *Daemon) handleCrewSleep(conn net.Conn, msg *protocol.CrewSleepMessage) {
	result, err := d.crewSleep(strings.TrimSpace(msg.Member))
	if err != nil {
		d.sendCrewError(conn, "sleep", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewSleepResult: result})
}

func (d *Daemon) handleCrewSleepWS(client *wsClient, msg *protocol.CrewSleepMessage) {
	result, err := d.crewSleep(strings.TrimSpace(msg.Member))
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

func (d *Daemon) crewSleep(name string) (*protocol.CrewSleepResult, error) {
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()

	member, doc, err := d.crewMember(name)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(member.BindingSession)
	if sessionID == "" {
		return &protocol.CrewSleepResult{
			Member:        member.ID,
			AlreadyAsleep: true,
			Detail:        fmt.Sprintf("%s is already asleep; no sleep request was sent", crew.DisplayName(member.ID)),
		}, nil
	}
	live, err := d.crewSessionActuallyLive(sessionID)
	if err != nil {
		return nil, fmt.Errorf("check %s's bound session %s: %w", crew.DisplayName(member.ID), shortSessionID(sessionID), err)
	}
	if !live {
		if _, err := d.releaseCrewBinding(member.ID, sessionID); err != nil {
			return nil, fmt.Errorf("release %s's exited session %s: %w", crew.DisplayName(member.ID), shortSessionID(sessionID), err)
		}
		d.noteCrewExitedSession(member.ID, sessionID)
		if restart, pending := pendingCrewRestartFor(member, sessionID); pending {
			if err := d.failCrewRestart(member.ID, restart.RequestID, sessionID, "", fmt.Errorf("session %s exited before the restart ran and %s was put to sleep", shortSessionID(sessionID), crew.DisplayName(member.ID))); err != nil {
				return nil, err
			}
		}
		return &protocol.CrewSleepResult{
			Member:        member.ID,
			AlreadyAsleep: true,
			Detail: fmt.Sprintf("%s is already asleep; previous session %s had exited and its binding was released; no sleep request was sent",
				crew.DisplayName(member.ID), shortSessionID(sessionID)),
		}, nil
	}

	now := time.Now()
	deliveryID := uuid.NewString()
	var delivery agentmailbox.Delivery
	if _, pending := pendingCrewRestartFor(member, sessionID); pending {
		member.Restart.State = crew.RestartFailed
		member.Restart.Withdrawn = true
		member.Restart.Error = fmt.Sprintf("the restart was withdrawn because the user asked %s to sleep instead", crew.DisplayName(member.ID))
		schema, schemaErr := d.crewCollection()
		if schemaErr != nil {
			return nil, schemaErr
		}
		body, encodeErr := member.Encode()
		if encodeErr != nil {
			return nil, encodeErr
		}
		fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.ID, false)
		written, committed, commitErr := d.store.CommitDocumentWriteWithMaintenancePrompt(
			store.DocumentWrite{Schema: *schema, ID: member.ID, Body: body, Expected: &doc.Rev},
			fact, deliveryID, sessionID, crewRequestedSleepPrompt, now,
		)
		if commitErr != nil {
			return nil, fmt.Errorf("record %s's sleep request: %w", crew.DisplayName(member.ID), commitErr)
		}
		d.announceCommittedWrite(fact, written.Seq)
		d.publishFact(FactCrewUpdated, member.ID, nil)
		delivery = committed
	} else {
		delivery, err = d.store.EnqueueMaintenancePrompt(deliveryID, sessionID, crewRequestedSleepPrompt, now)
	}
	if err != nil {
		return nil, fmt.Errorf("record %s's sleep request: %w", crew.DisplayName(member.ID), err)
	}
	status := protocol.AgentMsgStatusNotified
	detail := fmt.Sprintf("asked %s in session %s to write its handoff and file it with `attn handoff --sleep`", crew.DisplayName(member.ID), shortSessionID(sessionID))
	if err := d.deliverAgentMailboxItem(delivery); err != nil {
		status = protocol.AgentMsgStatusQueued
		detail = agentMessageQueuedDetail(err)
	}
	return &protocol.CrewSleepResult{
		Member:         member.ID,
		SessionID:      protocol.Ptr(sessionID),
		DeliveryStatus: protocol.Ptr(status),
		Detail:         detail,
	}, nil
}
