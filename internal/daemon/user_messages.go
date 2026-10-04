package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const FactCaptureChanged = "capture.changed"

func (d *Daemon) validateCaptureTarget(profileID string, target protocol.CaptureTarget) (inbox.Address, error) {
	switch target.Kind {
	case protocol.CaptureTargetKindChief:
		if target.MemberID != nil {
			return inbox.Address{}, fmt.Errorf("chief target cannot include member_id")
		}
	case protocol.CaptureTargetKindCrew:
		if strings.TrimSpace(protocol.Deref(target.MemberID)) == "" {
			return inbox.Address{}, fmt.Errorf("crew target requires member_id")
		}
		member, _, err := d.crewMember(*target.MemberID)
		if err != nil {
			return inbox.Address{}, err
		}
		if d.crewProfileID(member.ID) != profileID {
			return inbox.Address{}, fmt.Errorf("crew member %q not found in this profile", *target.MemberID)
		}
		if member.ID != *target.MemberID {
			return inbox.Address{}, fmt.Errorf("member_id must be the stable roster identity %q", member.ID)
		}
	default:
		return inbox.Address{}, fmt.Errorf("unknown capture target kind %q", target.Kind)
	}
	if target.Kind == protocol.CaptureTargetKindChief {
		return inbox.ToChief(profileID), nil
	}
	return inbox.ToMember(*target.MemberID), nil
}
func captureRequestID(msg any) string {
	switch m := msg.(type) {
	case *protocol.CaptureSendMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.CaptureGetMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.CaptureListMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.CaptureAttachmentPutMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.CaptureAttachmentGetMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.CaptureAttachmentDiscardMessage:
		return protocol.Deref(m.RequestID)
	}
	return ""
}
func (d *Daemon) captureRequest(profileID string, msg any, transportBytes int) (*protocol.CaptureResult, error) {
	if err := d.requireHome("Quick Capture"); err != nil {
		return nil, err
	}
	switch m := msg.(type) {
	case *protocol.CaptureSendMessage:
		if err := captureID(m.CaptureID); err != nil {
			return nil, err
		}
		if m.AttachmentIds == nil {
			m.AttachmentIds = []string{}
		}
		replay, err := d.store.CaptureReplay(profileID, *m)
		if err != nil {
			return nil, err
		}
		if !replay {
			to, err := d.validateCaptureTarget(profileID, m.Target)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(m.Content) == "" && len(m.AttachmentIds) == 0 {
				return nil, fmt.Errorf("capture needs text or at least one file")
			}
			seen := map[string]bool{}
			for _, id := range m.AttachmentIds {
				if err := captureID(id); err != nil {
					return nil, err
				}
				if seen[id] {
					return nil, fmt.Errorf("attachment %s repeated", id)
				}
				seen[id] = true
			}
			d.captureAssetMu.Lock()
			err = d.store.SaveCapture(profileID, *m, to, time.Now())
			d.captureAssetMu.Unlock()
			if err != nil {
				return nil, err
			}
			crashAt("capture-saved")
			d.publishCaptureChanged(profileID, m.CaptureID)
			d.kickInboxAfterCommit(to)
		}
		r, err := d.store.Capture(profileID, m.CaptureID)
		return &protocol.CaptureResult{Record: r}, err
	case *protocol.CaptureGetMessage:
		r, err := d.store.Capture(profileID, m.CaptureID)
		return &protocol.CaptureResult{Record: r}, err
	case *protocol.CaptureListMessage:
		items, next, err := d.store.Captures(profileID, m.Limit, protocol.Deref(m.Cursor))
		if err != nil {
			return nil, err
		}
		drafts, err := d.store.CaptureDraftAssets(profileID)
		return &protocol.CaptureResult{List: &protocol.CaptureListResult{Items: items, DraftAssets: drafts, NextCursor: next}}, err
	case *protocol.CaptureAttachmentPutMessage:
		upload, err := d.capturePut(profileID, m)
		return &protocol.CaptureResult{Upload: upload}, err
	case *protocol.CaptureAttachmentGetMessage:
		download, err := d.captureDownload(profileID, m, transportBytes/2)
		return &protocol.CaptureResult{Download: download}, err
	case *protocol.CaptureAttachmentDiscardMessage:
		err := d.captureDiscard(profileID, m)
		return &protocol.CaptureResult{Discarded: protocol.Ptr(err == nil)}, err
	}
	return nil, fmt.Errorf("unknown capture operation")
}
func (d *Daemon) handleCapture(conn net.Conn, msg any) {
	profileID, err := d.captureProfile(msg, "")
	var result *protocol.CaptureResult
	if err == nil {
		result, err = d.captureRequest(profileID, msg, maxInitialSocketFrameBytes)
	}
	response := protocol.Response{Ok: err == nil, CaptureResult: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrCaptureNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeCaptureNotFound)
		}
	}
	_ = json.NewEncoder(conn).Encode(response)
}
func (d *Daemon) handleCaptureWS(client *wsClient, selected string, msg any) {
	if _, download := msg.(*protocol.CaptureAttachmentGetMessage); !download && !client.isTrustedAppClient() {
		d.sendToClient(client, protocol.CaptureResultMessage{ProfileID: selected, Event: protocol.EventCaptureResult, RequestID: captureRequestID(msg), Success: false, Error: protocol.Ptr("capture authoring and history require the authenticated attn app"), ErrorCode: protocol.Ptr(protocol.ErrorCodeUnauthorizedClient)})
		return
	}
	profileID, err := d.captureProfile(msg, selected)
	var result *protocol.CaptureResult
	if err == nil {
		result, err = d.captureRequest(profileID, msg, int(websocketReadLimit(client)))
	}
	response := protocol.CaptureResultMessage{ProfileID: selected, Event: protocol.EventCaptureResult, RequestID: captureRequestID(msg), Success: err == nil, Result: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrCaptureNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeCaptureNotFound)
		}
	}
	d.sendToClient(client, response)
}

func (d *Daemon) captureProfile(msg any, selected string) (string, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	var scope struct {
		ProfileID       string `json:"profile_id"`
		SourceSessionID string `json:"source_session_id"`
	}
	if err := json.Unmarshal(raw, &scope); err != nil {
		return "", err
	}
	if selected != "" && scope.ProfileID != "" && scope.ProfileID != selected {
		return "", fmt.Errorf("capture belongs to profile %s; app selected profile %s", scope.ProfileID, selected)
	}
	profile, err := d.resolveGardenProfile(scope.SourceSessionID, scope.ProfileID, selected)
	if err != nil {
		return "", err
	}
	if selected != "" && profile.ID != selected {
		return "", fmt.Errorf("capture source session belongs to another profile")
	}
	return profile.ID, nil
}
func (d *Daemon) publishCaptureChanged(profileID, captureID string) {
	d.publishFact(FactCaptureChanged, captureID, map[string]string{"profile_id": profileID})
}
