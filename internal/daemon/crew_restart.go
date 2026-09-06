package daemon

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

var crewRequestedRestartPrompt = prompts.RenderText("crew", "restart-requested", prompts.Values{})

func crewRestartWire(restart *crew.Restart) *protocol.CrewRestart {
	if restart == nil {
		return nil
	}
	wire := &protocol.CrewRestart{
		RequestID: restart.RequestID, SessionID: restart.SessionID,
		State: protocol.CrewRestartState(restart.State),
	}
	if restart.DeliveryStatus != "" {
		status := protocol.AgentMsgStatus(restart.DeliveryStatus)
		wire.DeliveryStatus = &status
	}
	if restart.Detail != "" {
		wire.Detail = protocol.Ptr(restart.Detail)
	}
	if restart.Error != "" {
		wire.Error = protocol.Ptr(restart.Error)
	}
	if restart.LetterPath != "" {
		wire.LetterPath = protocol.Ptr(restart.LetterPath)
	}
	if restart.SuccessorSessionID != "" {
		wire.SuccessorSessionID = protocol.Ptr(restart.SuccessorSessionID)
	}
	return wire
}

func (d *Daemon) handleCrewRestart(conn net.Conn, msg *protocol.CrewRestartMessage) {
	result, err := d.crewRestart(strings.TrimSpace(msg.Member), strings.TrimSpace(msg.RequestID), msg.ExpectedSessionID)
	if err != nil {
		d.sendCrewError(conn, "restart", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRestartResult: result})
}

func (d *Daemon) handleCrewRestartWS(client *wsClient, msg *protocol.CrewRestartMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	result, err := d.crewRestart(strings.TrimSpace(msg.Member), requestID, msg.ExpectedSessionID)
	response := protocol.CrewRestartResultMessage{
		Event: protocol.EventCrewRestartResult, RequestID: requestID, Success: err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member = &result.Member
		response.Restart = &result.Restart
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewRestart(name, requestID string, expectedSessionID *string) (*protocol.CrewRestartResult, error) {
	if requestID == "" {
		return nil, fmt.Errorf("a request id is required so a retry cannot start two successors")
	}
	if expectedSessionID == nil {
		return nil, fmt.Errorf("the expected session id is required so a delayed request cannot restart a later day")
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()

	member, doc, err := d.crewMember(name)
	if err != nil {
		return nil, err
	}
	if member.Restart != nil && member.Restart.RequestID == requestID &&
		(member.Restart.State == crew.RestartCompleted || member.Restart.State == crew.RestartFailed) {
		return d.crewRestartResult(member, doc.Rev), nil
	}
	expected := strings.TrimSpace(*expectedSessionID)
	if expected != member.BindingSession {
		return nil, crewRestartDayChanged(member.ID, expected, member.BindingSession)
	}
	sessionID := strings.TrimSpace(member.BindingSession)
	if sessionID == "" {
		woken, wakeErr := d.crewWakeWithDeliveryLocked(member.ID, "", false, nil)
		if wakeErr != nil {
			return nil, wakeErr
		}
		updated, updateErr := d.setCrewRestart(member.ID, &crew.Restart{
			RequestID: requestID, SessionID: woken.SessionID, State: crew.RestartCompleted,
			SuccessorSessionID: woken.SessionID, Detail: fmt.Sprintf("%s was asleep and woke in session %s", crew.DisplayName(member.ID), shortSessionID(woken.SessionID)),
		})
		if updateErr != nil {
			return nil, updateErr
		}
		return d.crewRestartResultCurrent(updated.ID)
	}
	live, err := d.crewSessionActuallyLive(sessionID)
	if err != nil {
		return nil, fmt.Errorf("check %s's bound session %s: %w", crew.DisplayName(member.ID), shortSessionID(sessionID), err)
	}
	if !live {
		operation := &crew.Restart{RequestID: requestID, SessionID: sessionID}
		if member.Restart != nil && member.Restart.SessionID == sessionID &&
			(member.Restart.State == crew.RestartQueued || member.Restart.State == crew.RestartRequested) {
			copy := *member.Restart
			operation = &copy
		}
		if _, err := d.releaseCrewBinding(member.ID, sessionID); err != nil {
			return nil, err
		}
		woken, wakeErr := d.crewWakeWithDeliveryLocked(member.ID, "", false, nil)
		if wakeErr != nil {
			return nil, wakeErr
		}
		updated, updateErr := d.setCrewRestart(member.ID, &crew.Restart{
			RequestID: operation.RequestID, SessionID: sessionID, State: crew.RestartCompleted,
			SuccessorSessionID: woken.SessionID, Detail: fmt.Sprintf("released exited session %s and woke session %s", shortSessionID(sessionID), shortSessionID(woken.SessionID)),
		})
		if updateErr != nil {
			return nil, updateErr
		}
		return d.crewRestartResultCurrent(updated.ID)
	}
	if member.Restart != nil && member.Restart.RequestID == requestID {
		if member.Restart.State == crew.RestartQueued {
			if err := d.ensureCrewRestartRequest(member.ID, *member.Restart); err != nil {
				return nil, err
			}
			return d.crewRestartResultCurrent(member.ID)
		}
		return d.crewRestartResult(member, doc.Rev), nil
	}
	if member.Restart != nil && member.Restart.SessionID == sessionID &&
		(member.Restart.State == crew.RestartQueued || member.Restart.State == crew.RestartRequested) {
		if member.Restart.State == crew.RestartQueued {
			if err := d.ensureCrewRestartRequest(member.ID, *member.Restart); err != nil {
				return nil, err
			}
			return d.crewRestartResultCurrent(member.ID)
		}
		return d.crewRestartResult(member, doc.Rev), nil
	}

	// A failed turnover with a filed letter needs no second letter or prompt.
	if member.Restart != nil && member.Restart.State == crew.RestartFailed && member.Restart.SessionID == sessionID && member.Restart.LetterPath != "" {
		member.Restart.RequestID = requestID
		if member.LetterSession != sessionID || member.LetterPath == "" {
			member.LetterSession, member.LetterPath = sessionID, member.Restart.LetterPath
		}
		if _, err := d.writeCrewMemberMustCurrent(member, doc.Rev); err != nil {
			return nil, err
		}
		_, handoffErr := d.crewHandoff(sessionID, "", true, protocol.CrewDayCloseNap)
		if handoffErr != nil {
			return nil, handoffErr
		}
		return d.crewRestartResultCurrent(member.ID)
	}

	restart := &crew.Restart{RequestID: requestID, SessionID: sessionID, State: crew.RestartQueued}
	updated, err := d.setCrewRestart(member.ID, restart)
	if err != nil {
		return nil, err
	}
	if err := d.ensureCrewRestartRequest(updated.ID, *updated.Restart); err != nil {
		return nil, err
	}
	return d.crewRestartResultCurrent(member.ID)
}

func crewRestartMailboxID(memberID, requestID string) string {
	namespace := uuid.NewSHA1(uuid.NameSpaceOID, []byte(memberID+"\x00"+requestID))
	return "crew-restart-" + namespace.String()
}

func sessionOrAsleep(sessionID string) string {
	if sessionID == "" {
		return "asleep"
	}
	return shortSessionID(sessionID)
}

func crewRestartDayChanged(memberID, expected, current string) error {
	return fmt.Errorf("%s's day changed before the restart was applied: expected session %s, current session %s; refresh the roster and try again", crew.DisplayName(memberID), sessionOrAsleep(expected), sessionOrAsleep(current))
}

// ensureCrewRestartRequest repairs either side of a daemon crash. Its stable
// item id recognizes a read receipt too, so repair never asks twice.
func (d *Daemon) ensureCrewRestartRequest(memberID string, restart crew.Restart) error {
	delivery, _, err := d.store.EnqueueMaintenancePromptOnce(
		crewRestartMailboxID(memberID, restart.RequestID), restart.SessionID, restart.RequestID,
		"crew-restart-"+memberID, crewRequestedRestartPrompt, time.Now(),
	)
	if err != nil {
		cause := fmt.Errorf("record restart request: %w", err)
		if recordErr := d.failCrewRestart(memberID, restart.RequestID, restart.SessionID, "", cause); recordErr != nil {
			return errors.Join(cause, recordErr)
		}
		return cause
	}
	status := protocol.AgentMsgStatusNotified
	detail := fmt.Sprintf("asked %s in session %s to file its handoff and start the next day", crew.DisplayName(memberID), shortSessionID(restart.SessionID))
	if delivery.Item.ReadAt != "" {
		detail = fmt.Sprintf("%s read the restart request", crew.DisplayName(memberID))
	} else if err := d.deliverAgentMailboxItem(delivery); err != nil {
		status = protocol.AgentMsgStatusQueued
		detail = agentMessageQueuedDetail(err)
	}
	changed := false
	_, updateErr := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		changed = false
		if member.Restart == nil || member.Restart.RequestID != restart.RequestID || member.Restart.SessionID != restart.SessionID ||
			member.Restart.State == crew.RestartFailed || member.Restart.State == crew.RestartCompleted {
			return false, nil
		}
		if member.Restart.State == crew.RestartRequested {
			return false, nil
		}
		member.Restart.DeliveryStatus = string(status)
		member.Restart.Detail = detail
		if delivery.Item.ReadAt != "" {
			member.Restart.State = crew.RestartRequested
		}
		changed = true
		return true, nil
	})
	if updateErr != nil {
		return fmt.Errorf("record restart delivery for %s: %w", crew.DisplayName(memberID), updateErr)
	}
	if changed {
		d.publishFact(FactCrewUpdated, memberID, nil)
	}
	return nil
}

func (d *Daemon) reconcileCrewRestarts() {
	if err := d.requireHome(crew.Surface); err != nil {
		return
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		d.logf("crew: reconcile restart requests: %v", err)
		return
	}
	for _, member := range members {
		if member.Restart == nil || member.Restart.SessionID != member.BindingSession ||
			(member.Restart.State != crew.RestartQueued && member.Restart.State != crew.RestartRequested) {
			continue
		}
		if _, err := d.crewRestart(member.ID, member.Restart.RequestID, protocol.Ptr(member.BindingSession)); err != nil {
			d.logf("crew: reconcile %s's restart request: %v", crew.DisplayName(member.ID), err)
		}
	}
}

func (d *Daemon) writeCrewMemberMustCurrent(member crew.Member, revision int64) (crew.Member, error) {
	schema, err := d.crewCollection()
	if err != nil {
		return crew.Member{}, err
	}
	if _, err := d.writeCrewMember(*schema, member, revision); err != nil {
		return crew.Member{}, err
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	return member, nil
}

func (d *Daemon) setCrewRestart(memberID string, restart *crew.Restart) (crew.Member, error) {
	member, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		copy := *restart
		member.Restart = &copy
		return true, nil
	})
	if err == nil {
		d.publishFact(FactCrewUpdated, member.ID, nil)
	}
	return member, err
}

func (d *Daemon) crewRestartResult(member crew.Member, revision int64) *protocol.CrewRestartResult {
	wire := d.crewMemberWire(member, revision)
	return &protocol.CrewRestartResult{Member: wire, Restart: *wire.Restart}
}

func (d *Daemon) crewRestartResultCurrent(memberID string) (*protocol.CrewRestartResult, error) {
	member, doc, err := d.crewMember(memberID)
	if err != nil {
		return nil, err
	}
	return d.crewRestartResult(member, doc.Rev), nil
}

func (d *Daemon) failCrewRestart(memberID, requestID, sessionID, letter string, cause error) error {
	changed := false
	_, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		if member.Restart == nil || member.Restart.RequestID != requestID || member.Restart.SessionID != sessionID {
			return false, nil
		}
		if member.Restart.State == crew.RestartCompleted {
			return false, nil
		}
		member.Restart.State = crew.RestartFailed
		member.Restart.Error = cause.Error()
		if letter != "" {
			member.Restart.LetterPath = letter
		}
		changed = true
		return true, nil
	})
	if err != nil {
		recordErr := fmt.Errorf("record failed restart for %s: %w", crew.DisplayName(memberID), err)
		d.logf("crew: %v", recordErr)
		return recordErr
	}
	if changed {
		d.publishFact(FactCrewUpdated, memberID, nil)
	}
	return nil
}

func (d *Daemon) completeCrewRestart(memberID, requestID, sessionID, letter, successor string) {
	changed := false
	_, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		if member.Restart == nil || member.Restart.RequestID != requestID || member.Restart.SessionID != sessionID {
			return false, nil
		}
		if member.Restart.State == crew.RestartCompleted {
			return false, nil
		}
		member.Restart.State = crew.RestartCompleted
		member.Restart.Error = ""
		member.Restart.LetterPath = letter
		member.Restart.SuccessorSessionID = successor
		member.Restart.Detail = fmt.Sprintf("the handoff was filed and successor session %s started", shortSessionID(successor))
		changed = true
		return true, nil
	})
	if err != nil {
		d.logf("crew: record completed restart for %s: %v", crew.DisplayName(memberID), err)
		return
	}
	if changed {
		d.publishFact(FactCrewUpdated, memberID, nil)
	}
}

func (d *Daemon) noteCrewRestartMailboxRead(deliveries []agentmailbox.Delivery) {
	for _, delivery := range deliveries {
		if delivery.Item.Kind != agentmailbox.KindMaintenancePrompt || delivery.Item.SourceID == "" || !strings.HasPrefix(delivery.Item.CoalesceKey, "crew-restart-") {
			continue
		}
		members, _, err := d.readCrewMembers()
		if err != nil {
			continue
		}
		for _, member := range members {
			if member.Restart == nil || member.Restart.RequestID != delivery.Item.SourceID || member.Restart.SessionID != delivery.Item.RecipientSessionID || member.Restart.State != crew.RestartQueued {
				continue
			}
			changed := false
			_, updateErr := d.updateCrewMember(member.ID, func(current *crew.Member) (bool, error) {
				if current.Restart == nil || current.Restart.RequestID != delivery.Item.SourceID || current.Restart.SessionID != delivery.Item.RecipientSessionID || current.Restart.State != crew.RestartQueued {
					return false, nil
				}
				current.Restart.State = crew.RestartRequested
				current.Restart.DeliveryStatus = string(protocol.AgentMsgStatusNotified)
				current.Restart.Detail = fmt.Sprintf("%s read the restart request", crew.DisplayName(current.ID))
				changed = true
				return true, nil
			})
			if updateErr != nil {
				d.logf("crew: record %s's restart request read receipt: %v", crew.DisplayName(member.ID), updateErr)
			} else if changed {
				d.publishFact(FactCrewUpdated, member.ID, nil)
			}
		}
	}
}
