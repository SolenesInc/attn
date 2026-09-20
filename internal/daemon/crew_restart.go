package daemon

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
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
	result, err := d.crewRestart(strings.TrimSpace(msg.Member), strings.TrimSpace(msg.RequestID), msg.ExpectedSessionID, msg.ExpectedRevision)
	if err != nil {
		d.sendCrewError(conn, "restart", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRestartResult: result})
}

func (d *Daemon) handleCrewRestartWS(client *wsClient, msg *protocol.CrewRestartMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	result, err := d.crewRestart(strings.TrimSpace(msg.Member), requestID, msg.ExpectedSessionID, msg.ExpectedRevision)
	response := protocol.CrewRestartResultMessage{
		Event: protocol.EventCrewRestartResult, RequestID: requestID, Success: err == nil, Conflict: false,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		var conflict *crewRestartConflictError
		if errors.As(err, &conflict) {
			response.Conflict = true
			response.Member = &conflict.member
			response.Restart = conflict.member.Restart
		}
	} else {
		response.Member = &result.Member
		response.Restart = &result.Restart
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewRestart(name, requestID string, expectedSessionID *string, expectedRevision *int) (*protocol.CrewRestartResult, error) {
	if requestID == "" {
		return nil, fmt.Errorf("a request id is required so a retry cannot start two successors")
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()

	member, doc, err := d.crewMember(name)
	if err != nil {
		return nil, err
	}
	receipt, found, err := d.crewRestartRequest(member.ID, requestID)
	if err != nil {
		return nil, err
	}
	if found {
		if member.Restart != nil && member.Restart.RequestID == receipt.RestartRequestID {
			return d.resumeCrewRestart(member, doc.Rev)
		}
		return nil, d.crewRestartConflict(member, doc.Rev, fmt.Errorf("restart request %q was already accepted for an earlier day and will not restart the current day; inspect `attn crew list --json` for the current restart", requestID))
	}
	if member.Restart != nil && member.Restart.RequestID == requestID {
		if err := d.recordCrewRestartRequest(crew.RestartRequest{Member: member.ID, RequestID: requestID, RestartRequestID: requestID}); err != nil {
			return nil, err
		}
		return d.resumeCrewRestart(member, doc.Rev)
	}
	if expectedSessionID == nil {
		return nil, fmt.Errorf("the expected session id is required so a delayed request cannot restart a later day")
	}
	if expectedRevision == nil {
		return nil, fmt.Errorf("the expected revision is required so a delayed request cannot restart a later asleep period")
	}
	visibleSessionID := ""
	if d.crewBindingLive(member) {
		visibleSessionID = member.BindingSession
	}
	expected := strings.TrimSpace(*expectedSessionID)
	if expected != visibleSessionID {
		return nil, d.crewRestartConflict(member, doc.Rev, crewRestartDayChanged(member.ID, expected, visibleSessionID))
	}
	if int64(*expectedRevision) != doc.Rev {
		return nil, d.crewRestartConflict(member, doc.Rev, fmt.Errorf("%s's settings or day changed before the restart was applied: expected revision %d, current revision %d; refresh the roster and try again", crew.DisplayName(member.ID), *expectedRevision, doc.Rev))
	}
	if member.Restart != nil && member.Restart.State == crew.RestartFailed && member.Restart.Withdrawn &&
		member.Restart.SessionID == visibleSessionID && visibleSessionID != "" {
		live, liveErr := d.crewSessionActuallyLive(visibleSessionID)
		if liveErr != nil {
			return nil, fmt.Errorf("check %s's closing session %s: %w", crew.DisplayName(member.ID), shortSessionID(visibleSessionID), liveErr)
		}
		if live {
			return nil, d.crewRestartConflict(member, doc.Rev, fmt.Errorf("%s's day in session %s is still closing after a sleep request; wait until the member is asleep, then retry this restart", crew.DisplayName(member.ID), shortSessionID(visibleSessionID)))
		}
	}
	if member.Restart != nil && member.Restart.SessionID == visibleSessionID &&
		(member.Restart.State == crew.RestartQueued || member.Restart.State == crew.RestartRequested) {
		if err := d.recordCrewRestartRequest(crew.RestartRequest{Member: member.ID, RequestID: requestID, RestartRequestID: member.Restart.RequestID}); err != nil {
			return nil, err
		}
		return d.resumeCrewRestart(member, doc.Rev)
	}
	receipts, err := d.crewRestartAcceptanceReceipts(member, requestID)
	if err != nil {
		return nil, err
	}

	if member.Restart != nil && member.Restart.State == crew.RestartFailed && member.Restart.SessionID == visibleSessionID && member.Restart.LetterPath != "" {
		candidate := member
		retry := *member.Restart
		retry.RequestID = requestID
		retry.State = crew.RestartQueued
		retry.Error = ""
		retry.Withdrawn = false
		candidate.Restart = &retry
		if candidate.LetterSession != visibleSessionID || candidate.LetterPath == "" {
			candidate.LetterSession, candidate.LetterPath = visibleSessionID, retry.LetterPath
		}
		if err := d.recordCrewRestart(candidate, doc.Rev, receipts...); err != nil {
			return nil, err
		}
		return d.resumeCrewRestartCurrent(candidate.ID)
	}

	candidate := member
	candidate.Restart = &crew.Restart{RequestID: requestID, SessionID: visibleSessionID, State: crew.RestartQueued}
	if err := d.recordCrewRestart(candidate, doc.Rev, receipts...); err != nil {
		return nil, err
	}
	return d.resumeCrewRestartCurrent(candidate.ID)
}

type crewRestartConflictError struct {
	member protocol.CrewMember
	cause  error
}

func (e *crewRestartConflictError) Error() string { return e.cause.Error() }
func (e *crewRestartConflictError) Unwrap() error { return e.cause }

func (d *Daemon) crewRestartConflict(member crew.Member, revision int64, cause error) error {
	return &crewRestartConflictError{member: d.crewMemberWire(member, revision), cause: cause}
}

func crewRestartRequestDocumentID(memberID, requestID string) string {
	return fmt.Sprintf("r-%x", sha256.Sum256([]byte(memberID+"\x00"+requestID)))
}

func (d *Daemon) crewRestartRequest(memberID, requestID string) (crew.RestartRequest, bool, error) {
	schema, err := d.crewRestartRequestsCollection()
	if err != nil {
		return crew.RestartRequest{}, false, err
	}
	doc, found, err := d.store.GetDocument(*schema, crewRestartRequestDocumentID(memberID, requestID))
	if err != nil || !found {
		return crew.RestartRequest{}, found, err
	}
	receipt, err := crew.DecodeRestartRequest(doc.Body)
	if err != nil {
		return crew.RestartRequest{}, false, err
	}
	if receipt.Member != memberID || receipt.RequestID != requestID || receipt.RestartRequestID == "" {
		return crew.RestartRequest{}, false, fmt.Errorf("stored restart request %q for %s does not match its address", requestID, crew.DisplayName(memberID))
	}
	return receipt, true, nil
}

func (d *Daemon) recordCrewRestartRequest(receipt crew.RestartRequest) error {
	schema, err := d.crewRestartRequestsCollection()
	if err != nil {
		return err
	}
	body, err := receipt.Encode()
	if err != nil {
		return err
	}
	absent := docstore.ExpectAbsent
	fact := documentChangedFact(crew.Namespace, crew.CollectionRestartRequests, crewRestartRequestDocumentID(receipt.Member, receipt.RequestID), false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: crewRestartRequestDocumentID(receipt.Member, receipt.RequestID), Body: body, Expected: &absent,
	}, fact, time.Now())
	if err != nil {
		return err
	}
	d.announceCommittedWrite(fact, written.Seq)
	return nil
}

func (d *Daemon) crewRestartAcceptanceReceipts(member crew.Member, requestID string) ([]crew.RestartRequest, error) {
	receipts := []crew.RestartRequest{{Member: member.ID, RequestID: requestID, RestartRequestID: requestID}}
	if member.Restart == nil {
		return receipts, nil
	}
	_, found, err := d.crewRestartRequest(member.ID, member.Restart.RequestID)
	if err != nil {
		return nil, err
	}
	if !found {
		receipts = append(receipts, crew.RestartRequest{Member: member.ID, RequestID: member.Restart.RequestID, RestartRequestID: member.Restart.RequestID})
	}
	return receipts, nil
}

func (d *Daemon) recordCrewRestart(member crew.Member, revision int64, receipts ...crew.RestartRequest) error {
	memberSchema, err := d.crewCollection()
	if err != nil {
		return err
	}
	receiptSchema, err := d.crewRestartRequestsCollection()
	if err != nil {
		return err
	}
	if err := d.validateCrewMemberPaths(member); err != nil {
		return err
	}
	memberBody, err := member.Encode()
	if err != nil {
		return err
	}
	memberFact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.ID, false)
	commits := []store.DocumentCommit{{
		Write: store.DocumentWrite{Schema: *memberSchema, ID: member.ID, Body: memberBody, Expected: &revision},
		Fact:  memberFact,
	}}
	absent := docstore.ExpectAbsent
	for _, receipt := range receipts {
		body, encodeErr := receipt.Encode()
		if encodeErr != nil {
			return encodeErr
		}
		id := crewRestartRequestDocumentID(receipt.Member, receipt.RequestID)
		commits = append(commits, store.DocumentCommit{
			Write: store.DocumentWrite{Schema: *receiptSchema, ID: id, Body: body, Expected: &absent},
			Fact:  documentChangedFact(crew.Namespace, crew.CollectionRestartRequests, id, false),
		})
	}
	written, err := d.store.CommitDocumentWrites(commits, time.Now())
	if err != nil {
		if !docstore.IsConflict(err) {
			return err
		}
		current, doc, readErr := d.crewMember(member.ID)
		if readErr != nil {
			return errors.Join(err, readErr)
		}
		return d.crewRestartConflict(current, doc.Rev, fmt.Errorf("%s changed while its restart was being recorded; refresh the roster and try again: %w", crew.DisplayName(member.ID), err))
	}
	for i, commit := range commits {
		d.announceCommittedWrite(commit.Fact, written[i].Seq)
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	return nil
}

func (d *Daemon) resumeCrewRestartCurrent(memberID string) (*protocol.CrewRestartResult, error) {
	member, doc, err := d.crewMember(memberID)
	if err != nil {
		return nil, err
	}
	return d.resumeCrewRestart(member, doc.Rev)
}

func (d *Daemon) resumeCrewRestart(member crew.Member, revision int64) (*protocol.CrewRestartResult, error) {
	restart := member.Restart
	if restart == nil || restart.State == crew.RestartCompleted || restart.State == crew.RestartFailed {
		return d.crewRestartResult(member, revision), nil
	}
	if restart.State != crew.RestartQueued && restart.State != crew.RestartRequested {
		return d.crewRestartResult(member, revision), nil
	}
	if restart.SessionID == member.BindingSession {
		if restart.SessionID == "" {
			return d.wakeForCrewRestart(member, *restart, "")
		}
		live, err := d.crewSessionActuallyLive(restart.SessionID)
		if err != nil {
			return nil, fmt.Errorf("check %s's bound session %s: %w", crew.DisplayName(member.ID), shortSessionID(restart.SessionID), err)
		}
		if !live {
			if _, err := d.releaseCrewBinding(member.ID, restart.SessionID); err != nil {
				return nil, err
			}
			return d.wakeForCrewRestart(member, *restart, restart.SessionID)
		}
		if restart.LetterPath != "" {
			if _, err := d.crewHandoffLocked(restart.SessionID, "", true, protocol.CrewDayCloseNap); err != nil {
				return nil, err
			}
			return d.crewRestartResultCurrent(member.ID)
		}
		if err := d.ensureCrewRestartRequest(member.ID, *restart); err != nil {
			return nil, err
		}
		return d.crewRestartResultCurrent(member.ID)
	}
	if restart.SessionID == "" {
		if member.BindingSession != "" {
			live, err := d.crewSessionActuallyLive(member.BindingSession)
			if err != nil {
				return nil, fmt.Errorf("check %s's possible wake session %s: %w", crew.DisplayName(member.ID), shortSessionID(member.BindingSession), err)
			}
			if !live {
				if _, err := d.releaseCrewBinding(member.ID, member.BindingSession); err != nil {
					return nil, err
				}
				return d.wakeForCrewRestart(member, *restart, member.BindingSession)
			}
			d.completeCrewRestartWithDetail(member.ID, restart.RequestID, "", "", member.BindingSession,
				fmt.Sprintf("%s woke in session %s", crew.DisplayName(member.ID), shortSessionID(member.BindingSession)))
			return d.crewRestartResultCurrent(member.ID)
		}
		return d.wakeForCrewRestart(member, *restart, "")
	}

	if member.BindingSession == "" {
		return d.wakeForCrewRestart(member, *restart, restart.SessionID)
	}
	letter, hasLetter, letterErr := d.crewRestartFiledLetter(member, *restart)
	liveSuccessor := false
	if member.BindingSession != "" {
		live, err := d.crewSessionActuallyLive(member.BindingSession)
		if err != nil {
			return nil, fmt.Errorf("check %s's successor session %s: %w", crew.DisplayName(member.ID), shortSessionID(member.BindingSession), err)
		}
		liveSuccessor = live
	}
	if letterErr == nil && hasLetter && liveSuccessor {
		d.completeCrewRestartWithDetail(member.ID, restart.RequestID, restart.SessionID, letter, member.BindingSession,
			fmt.Sprintf("the filed handoff was recovered with successor session %s", shortSessionID(member.BindingSession)))
		return d.crewRestartResultCurrent(member.ID)
	}
	cause := letterErr
	if cause == nil {
		cause = fmt.Errorf("the restart moved from session %s to %s, but a live successor and readable filed letter could not both be proven; retry the turnover from the roster", shortSessionID(restart.SessionID), sessionOrAsleep(member.BindingSession))
	}
	if err := d.failCrewRestart(member.ID, restart.RequestID, restart.SessionID, letter, cause); err != nil {
		return nil, err
	}
	return d.crewRestartResultCurrent(member.ID)
}

func (d *Daemon) wakeForCrewRestart(member crew.Member, restart crew.Restart, exitedSessionID string) (*protocol.CrewRestartResult, error) {
	woken, err := d.crewWakeWithDeliveryLocked(member.ID, "", false, nil)
	if err != nil {
		if recordErr := d.failCrewRestart(member.ID, restart.RequestID, restart.SessionID, "", err); recordErr != nil {
			return nil, errors.Join(err, recordErr)
		}
		return nil, err
	}
	detail := fmt.Sprintf("%s was asleep and woke in session %s", crew.DisplayName(member.ID), shortSessionID(woken.SessionID))
	if exitedSessionID != "" {
		detail = fmt.Sprintf("released exited session %s and woke session %s", shortSessionID(exitedSessionID), shortSessionID(woken.SessionID))
	}
	d.completeCrewRestartWithDetail(member.ID, restart.RequestID, restart.SessionID, "", woken.SessionID, detail)
	return d.crewRestartResultCurrent(member.ID)
}

func (d *Daemon) crewRestartFiledLetter(member crew.Member, restart crew.Restart) (string, bool, error) {
	path, ok := member.FiledLetterFor(restart.SessionID)
	if !ok && restart.LetterPath != "" {
		path, ok = restart.LetterPath, true
	}
	if !ok {
		return "", false, nil
	}
	if err := d.validateCrewLetterPath(member, path); err != nil {
		return path, true, err
	}
	if _, err := os.Stat(path); err != nil {
		return path, true, fmt.Errorf("the filed handoff at %s is not readable: %w", path, err)
	}
	return path, true, nil
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
	_, updateErr := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
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
		return true, nil
	})
	if updateErr != nil {
		return fmt.Errorf("record restart delivery for %s: %w", crew.DisplayName(memberID), updateErr)
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
		if member.Restart == nil ||
			(member.Restart.State != crew.RestartQueued && member.Restart.State != crew.RestartRequested) {
			continue
		}
		if _, err := d.crewRestart(member.ID, member.Restart.RequestID, nil, nil); err != nil {
			d.logf("crew: reconcile %s's restart request: %v", crew.DisplayName(member.ID), err)
		}
	}
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

func pendingCrewRestartFor(member crew.Member, sessionID string) (crew.Restart, bool) {
	restart := member.Restart
	if restart == nil || restart.SessionID != sessionID ||
		(restart.State != crew.RestartQueued && restart.State != crew.RestartRequested) {
		return crew.Restart{}, false
	}
	return *restart, true
}

func (d *Daemon) failCrewRestart(memberID, requestID, sessionID, letter string, cause error) error {
	_, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		if member.Restart == nil || member.Restart.RequestID != requestID || member.Restart.SessionID != sessionID {
			return false, nil
		}
		if member.Restart.State == crew.RestartCompleted || member.Restart.Withdrawn {
			return false, nil
		}
		member.Restart.State = crew.RestartFailed
		member.Restart.Error = cause.Error()
		if letter != "" {
			member.Restart.LetterPath = letter
		}
		return true, nil
	})
	if err != nil {
		recordErr := fmt.Errorf("record failed restart for %s: %w", crew.DisplayName(memberID), err)
		d.logf("crew: %v", recordErr)
		return recordErr
	}
	return nil
}

func (d *Daemon) completeCrewRestart(memberID, requestID, sessionID, letter, successor string) {
	d.completeCrewRestartWithDetail(memberID, requestID, sessionID, letter, successor,
		fmt.Sprintf("the handoff was filed and successor session %s started", shortSessionID(successor)))
}

func (d *Daemon) completeCrewRestartWithDetail(memberID, requestID, sessionID, letter, successor, detail string) {
	_, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		if member.Restart == nil || member.Restart.RequestID != requestID || member.Restart.SessionID != sessionID {
			return false, nil
		}
		if member.Restart.State == crew.RestartCompleted || member.Restart.Withdrawn {
			return false, nil
		}
		member.Restart.State = crew.RestartCompleted
		member.Restart.Error = ""
		member.Restart.LetterPath = letter
		member.Restart.SuccessorSessionID = successor
		member.Restart.Detail = detail
		return true, nil
	})
	if err != nil {
		d.logf("crew: record completed restart for %s: %v", crew.DisplayName(memberID), err)
		return
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
			_, updateErr := d.updateCrewMember(member.ID, func(current *crew.Member) (bool, error) {
				if current.Restart == nil || current.Restart.RequestID != delivery.Item.SourceID || current.Restart.SessionID != delivery.Item.RecipientSessionID || current.Restart.State != crew.RestartQueued {
					return false, nil
				}
				current.Restart.State = crew.RestartRequested
				current.Restart.DeliveryStatus = string(protocol.AgentMsgStatusNotified)
				current.Restart.Detail = fmt.Sprintf("%s read the restart request", crew.DisplayName(current.ID))
				return true, nil
			})
			if updateErr != nil {
				d.logf("crew: record %s's restart request read receipt: %v", crew.DisplayName(member.ID), updateErr)
			}
		}
	}
}
