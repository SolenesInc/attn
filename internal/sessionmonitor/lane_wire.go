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
	return protocol.SessionPlaceMessage{Cmd: protocol.CmdSessionPlace, SessionID: session, DeliveryID: delivery.ID.String(), Text: delivery.Text, Phase: phases[delivery.Placement], Origin: origin, Source: protocol.Ptr(delivery.origin.source), BypassInitialGate: protocol.Ptr(delivery.BypassInitialGate), AllowUserComposer: protocol.Ptr(delivery.AllowUserComposer)}
}

func DeliveryFromMessage(msg protocol.SessionPlaceMessage) Delivery {
	domain, key, _ := strings.Cut(msg.DeliveryID, "/")
	phases := map[protocol.SessionPlacePhase]Placement{protocol.SessionPlacePhasePromptReady: WhenPromptReady, protocol.SessionPlacePhaseWorkingOrPromptReady: WhenWorkingOrPromptReady, protocol.SessionPlacePhaseTurnBoundary: AtTurnBoundary}
	origin := inputOrigin{kind: originMaintenance, source: protocol.Deref(msg.Source)}
	if msg.Origin == protocol.SessionPlaceOriginUserConversation {
		origin.kind = originUserConversation
	}
	return Delivery{ID: NewAttemptID(domain, key), Text: msg.Text, Placement: phases[msg.Phase], origin: origin, BypassInitialGate: protocol.Deref(msg.BypassInitialGate), AllowUserComposer: protocol.Deref(msg.AllowUserComposer)}
}

func (attempt Attempt) Result() protocol.SessionPlaceResult {
	stages := map[Stage]protocol.SessionPlaceStage{Placed: protocol.SessionPlaceStagePlaced, Deferred: protocol.SessionPlaceStageDeferred, Failed: protocol.SessionPlaceStageFailed}
	result := protocol.SessionPlaceResult{Stage: stages[attempt.Stage]}
	if !attempt.At.IsZero() {
		result.At = protocol.Ptr(attempt.At.Format(time.RFC3339Nano))
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
		{errPlacingAnother, protocol.SessionPlaceErrorKindPlacingAnother},
		{ErrBlockedByApproval, protocol.SessionPlaceErrorKindBlockedByApproval},
		{ErrComposerDirty, protocol.SessionPlaceErrorKindComposerDirty},
		{errInitialPrompt, protocol.SessionPlaceErrorKindInitialPrompt},
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
	attempt := Attempt{Stage: stages[result.Stage]}
	if result.At != nil {
		attempt.At, _ = time.Parse(time.RFC3339Nano, *result.At)
	}
	if result.Error == nil {
		return attempt
	}
	switch protocol.Deref(result.ErrorKind) {
	case protocol.SessionPlaceErrorKindLaneClosed:
		attempt.Err = ErrLaneClosed
	case protocol.SessionPlaceErrorKindPlacingAnother:
		attempt.Err = errPlacingAnother
	case protocol.SessionPlaceErrorKindBlockedByApproval:
		attempt.Err = ErrBlockedByApproval
	case protocol.SessionPlaceErrorKindComposerDirty:
		if result.RetryAfterMs != nil {
			attempt.Err = &quietError{retryAfter: time.Duration(*result.RetryAfterMs) * time.Millisecond}
		} else {
			attempt.Err = ErrComposerDirty
		}
	case protocol.SessionPlaceErrorKindInitialPrompt:
		attempt.Err = errInitialPrompt
	default:
		attempt.Err = errors.New(*result.Error)
	}
	return attempt
}
