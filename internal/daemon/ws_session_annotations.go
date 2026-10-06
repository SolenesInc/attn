package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleSessionAnnotationsGet(client *wsClient, msg *protocol.SessionAnnotationsGetMessage) {
	handler := newAnnotationDraftHandler(d, client, sessionAnnotationDraftAccessors(d.store), "session_id",
		func(result annotationDraftResult[protocol.SessionAnnotation]) protocol.SessionAnnotationsGetResultMessage {
			return protocol.SessionAnnotationsGetResultMessage{
				Event:       protocol.EventSessionAnnotationsGetResult,
				RequestID:   msg.RequestID,
				SessionID:   protocol.SessionID(result.key),
				Annotations: result.annotations,
				Note:        result.note,
				Generation:  result.generation,
				Success:     result.success,
				Error:       result.err,
			}
		})
	handler.get("session_annotations_get", string(msg.SessionID), decodeSessionAnnotations)
}

func (d *Daemon) handleSessionAnnotationsSave(client *wsClient, msg *protocol.SessionAnnotationsSaveMessage) {
	annotations := msg.Annotations
	if annotations == nil {
		annotations = []protocol.SessionAnnotation{}
	}
	handler := newAnnotationDraftHandler(d, client, sessionAnnotationDraftAccessors(d.store), "session_id",
		func(result annotationDraftResult[protocol.SessionAnnotation]) protocol.SessionAnnotationsSaveResultMessage {
			return protocol.SessionAnnotationsSaveResultMessage{
				Event:      protocol.EventSessionAnnotationsSaveResult,
				RequestID:  msg.RequestID,
				SessionID:  protocol.SessionID(result.key),
				Generation: result.generation,
				Success:    result.success,
				Stale:      result.stale,
				Error:      result.err,
			}
		})
	handler.save("session_annotations_save", string(msg.SessionID), annotations, protocol.Deref(msg.Note), msg.Generation)
}

func (d *Daemon) handleSessionAnnotationsClear(client *wsClient, msg *protocol.SessionAnnotationsClearMessage) {
	handler := newAnnotationDraftHandler(d, client, sessionAnnotationDraftAccessors(d.store), "session_id",
		func(result annotationDraftResult[protocol.SessionAnnotation]) protocol.SessionAnnotationsClearResultMessage {
			return protocol.SessionAnnotationsClearResultMessage{
				Event:      protocol.EventSessionAnnotationsClearResult,
				RequestID:  msg.RequestID,
				SessionID:  protocol.SessionID(result.key),
				Generation: result.generation,
				Success:    result.success,
				Error:      result.err,
			}
		})
	handler.clear("session_annotations_clear", string(msg.SessionID), msg.Generation)
}

func (d *Daemon) handleSessionAnnotationsSubmit(client *wsClient, msg *protocol.SessionAnnotationsSubmitMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	result := protocol.SessionAnnotationsSubmitResultMessage{
		Event:     protocol.EventSessionAnnotationsSubmitResult,
		RequestID: msg.RequestID,
		SessionID: sessionID,
		Status:    annotationSubmitStatusError,
	}
	fail := func(errText string) {
		result.Error = protocol.Ptr(errText)
		d.sendToClient(client, result)
	}
	if sessionID == "" {
		fail("session_annotations_submit: session_id is required")
		return
	}
	if strings.TrimSpace(msg.Text) == "" {
		fail("session_annotations_submit: text is required")
		return
	}
	if d.store.Get(sessionID) == nil {
		fail(string("session_annotations_submit: unknown session " + sessionID))
		return
	}
	delivery := annotationSessionInput(msg.RequestID, sessionID, msg.Text)
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if attempt.stage != sessionInputPlaced {
		if sessionInputDeferredError(attempt.err) {
			result.Status = annotationSubmitStatusSkipped
			d.sendToClient(client, result)
			return
		}
		d.logf("session_annotations_submit: %s: delivery failed: %v", sessionID, attempt.err)
		fail(attempt.err.Error())
		return
	}
	result.Success = true
	result.Status = annotationSubmitStatusDelivered
	d.sendToClient(client, result)
}

func decodeSessionAnnotations(raw string) ([]protocol.SessionAnnotation, error) {
	if strings.TrimSpace(raw) == "" {
		return []protocol.SessionAnnotation{}, nil
	}
	var annotations []protocol.SessionAnnotation
	if err := json.Unmarshal([]byte(raw), &annotations); err != nil {
		return nil, err
	}
	if annotations == nil {
		annotations = []protocol.SessionAnnotation{}
	}
	return annotations, nil
}
