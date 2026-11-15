package sessionmonitor

import (
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (delivery Delivery) Message(session protocol.SessionID) protocol.SessionPlaceMessage {
	phases := map[Placement]protocol.SessionPlacePhase{WhenPromptReady: protocol.SessionPlacePhasePromptReady, WhenWorkingOrPromptReady: protocol.SessionPlacePhaseWorkingOrPromptReady, AtTurnBoundary: protocol.SessionPlacePhaseTurnBoundary}
	origin := protocol.SessionPlaceOriginMaintenance
	if delivery.origin.kind == originUserConversation {
		origin = protocol.SessionPlaceOriginUserConversation
	}
	msg := protocol.SessionPlaceMessage{Cmd: protocol.CmdSessionPlace, SessionID: session, DeliveryID: delivery.ID.String(), Text: delivery.Text, Phase: phases[delivery.Placement], Origin: origin, Source: protocol.Ptr(string(delivery.origin.source)), BypassInitialGate: protocol.Ptr(delivery.BypassInitialGate), AllowUserComposer: protocol.Ptr(delivery.AllowUserComposer), Compact: protocol.Ptr(delivery.Compact), Ring: delivery.Ring}
	if delivery.UserWords {
		msg.UserWords = protocol.Ptr(true)
	}
	if delivery.ExpectedDriverRun != "" {
		msg.ExpectedDriverRun = protocol.Ptr(delivery.ExpectedDriverRun)
	}
	if delivery.ExpectedConversation != "" {
		msg.ExpectedConversation = protocol.Ptr(delivery.ExpectedConversation)
	}
	return msg
}

func DeliveryFromMessage(msg protocol.SessionPlaceMessage) Delivery {
	domain, key, _ := strings.Cut(msg.DeliveryID, "/")
	phases := map[protocol.SessionPlacePhase]Placement{protocol.SessionPlacePhasePromptReady: WhenPromptReady, protocol.SessionPlacePhaseWorkingOrPromptReady: WhenWorkingOrPromptReady, protocol.SessionPlacePhaseTurnBoundary: AtTurnBoundary}
	origin := inputOrigin{kind: originMaintenance, source: Maintenance(protocol.Deref(msg.Source))}
	if msg.Origin == protocol.SessionPlaceOriginUserConversation {
		origin.kind = originUserConversation
	}
	return Delivery{UserWords: protocol.Deref(msg.UserWords), ID: NewAttemptID(domain, key), Text: msg.Text, Placement: phases[msg.Phase], origin: origin, BypassInitialGate: protocol.Deref(msg.BypassInitialGate), AllowUserComposer: protocol.Deref(msg.AllowUserComposer), Compact: protocol.Deref(msg.Compact), Ring: msg.Ring, ExpectedDriverRun: protocol.Deref(msg.ExpectedDriverRun), ExpectedConversation: protocol.Deref(msg.ExpectedConversation)}
}

func (attempt Attempt) Result() protocol.SessionPlaceResult {
	stages := map[Stage]protocol.SessionPlaceStage{Placed: protocol.SessionPlaceStagePlaced, Deferred: protocol.SessionPlaceStageDeferred, Failed: protocol.SessionPlaceStageFailed}
	result := protocol.SessionPlaceResult{Stage: stages[attempt.Stage]}
	if attempt.Uncertain {
		result.Uncertain = protocol.Ptr(true)
	}
	if !attempt.At.IsZero() {
		result.At = protocol.Ptr(attempt.At.Format(time.RFC3339Nano))
	}
	if attempt.Held {
		result.Held = protocol.Ptr(true)
	}
	if attempt.Err == nil {
		return result
	}
	result.Error = protocol.Ptr(attempt.Err.Error())
	for _, entry := range []struct {
		err  error
		kind protocol.SessionPlaceErrorKind
	}{
		{ErrLaneClosed, protocol.SessionPlaceErrorKindLaneClosed},
		{ErrBlockedByApproval, protocol.SessionPlaceErrorKindBlockedByApproval},
		{ErrUserTyping, protocol.SessionPlaceErrorKindUserTyping},
		{errInitialPrompt, protocol.SessionPlaceErrorKindInitialPrompt},
		{errHarnessStarting, protocol.SessionPlaceErrorKindHarnessStarting},
		{ErrSessionStopped, protocol.SessionPlaceErrorKindSessionStopped},
		{ErrSessionClosing, protocol.SessionPlaceErrorKindSessionClosing},
	} {
		if errors.Is(attempt.Err, entry.err) {
			result.ErrorKind = &entry.kind
			break
		}
	}
	if after, retry := RetryDelay(attempt.Err); retry {
		result.RetryAfterMs = protocol.Ptr(int((after + time.Millisecond - 1) / time.Millisecond))
	}
	return result
}

func AttemptFromResult(result protocol.SessionPlaceResult) Attempt {
	stages := map[protocol.SessionPlaceStage]Stage{protocol.SessionPlaceStagePlaced: Placed, protocol.SessionPlaceStageDeferred: Deferred, protocol.SessionPlaceStageFailed: Failed}
	attempt := Attempt{Stage: stages[result.Stage], Held: protocol.Deref(result.Held), Uncertain: protocol.Deref(result.Uncertain)}
	if result.At != nil {
		attempt.At, _ = time.Parse(time.RFC3339Nano, *result.At)
	}
	if result.Error == nil {
		return attempt
	}
	switch protocol.Deref(result.ErrorKind) {
	case protocol.SessionPlaceErrorKindLaneClosed:
		attempt.Err = ErrLaneClosed
	case protocol.SessionPlaceErrorKindBlockedByApproval:
		attempt.Err = ErrBlockedByApproval
	case protocol.SessionPlaceErrorKindUserTyping:
		if result.RetryAfterMs != nil {
			attempt.Err = &quietError{retryAfter: time.Duration(*result.RetryAfterMs) * time.Millisecond}
		} else {
			attempt.Err = ErrUserTyping
		}
	case protocol.SessionPlaceErrorKindInitialPrompt:
		attempt.Err = errInitialPrompt
	case protocol.SessionPlaceErrorKindHarnessStarting:
		attempt.Err = errHarnessStarting
	case protocol.SessionPlaceErrorKindSessionStopped:
		attempt.Err = ErrSessionStopped
	case protocol.SessionPlaceErrorKindSessionClosing:
		attempt.Err = ErrSessionClosing
	default:
		attempt.Err = errors.New(*result.Error)
	}
	return attempt
}
