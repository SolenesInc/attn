package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const FactCaptureChanged = "capture.changed"

func (d *Daemon) validateCaptureTarget(target protocol.CaptureTarget) (inbox.Address, error) {
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
		if member.ID != *target.MemberID {
			return inbox.Address{}, fmt.Errorf("member_id must be the stable roster identity %q", member.ID)
		}
	default:
		return inbox.Address{}, fmt.Errorf("unknown capture target kind %q", target.Kind)
	}
	if target.Kind == protocol.CaptureTargetKindChief {
		return inbox.ToChief(), nil
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
func (d *Daemon) captureRequest(msg any, transportBytes int) (*protocol.CaptureResult, error) {
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
		replay, err := d.store.CaptureReplay(*m)
		if err != nil {
			return nil, err
		}
		if !replay {
			to, err := d.validateCaptureTarget(m.Target)
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
			replay, err = d.store.CaptureReplay(*m)
			if !replay && err == nil {
				for _, id := range m.AttachmentIds {
					a, assetErr := d.store.CaptureAsset(m.CaptureID, id)
					if assetErr != nil {
						err = assetErr
						break
					}
					if a == nil || a.State != "ready" {
						err = fmt.Errorf("attachment %s is not ready", id)
						break
					}
					_, path := d.captureAssetPaths(m.CaptureID, id)
					info, fileErr := os.Stat(path)
					if fileErr != nil {
						err = fileErr
						break
					}
					if int(info.Size()) != a.Attachment.Bytes {
						err = fmt.Errorf("attachment %s bytes=%d, found %d", id, a.Attachment.Bytes, info.Size())
						break
					}
				}
				if err == nil {
					err = d.store.SaveCapture(*m, to, time.Now())
				}
			}
			d.captureAssetMu.Unlock()
			if err != nil {
				return nil, err
			}
			if !replay {
				crashAt("capture-saved")
				d.publishFact(FactCaptureChanged, m.CaptureID, nil)
				d.kickInboxAfterCommit(to)
			}
		}
		r, err := d.store.Capture(m.CaptureID)
		return &protocol.CaptureResult{Record: r}, err
	case *protocol.CaptureGetMessage:
		r, err := d.store.Capture(m.CaptureID)
		return &protocol.CaptureResult{Record: r}, err
	case *protocol.CaptureListMessage:
		items, next, err := d.store.Captures(m.Limit, protocol.Deref(m.Cursor))
		if err != nil {
			return nil, err
		}
		drafts, err := d.store.CaptureDraftAssets()
		return &protocol.CaptureResult{List: &protocol.CaptureListResult{Items: items, DraftAssets: drafts, NextCursor: next}}, err
	case *protocol.CaptureAttachmentPutMessage:
		upload, err := d.capturePut(m)
		return &protocol.CaptureResult{Upload: upload}, err
	case *protocol.CaptureAttachmentGetMessage:
		download, err := d.captureDownload(m, transportBytes/2)
		return &protocol.CaptureResult{Download: download}, err
	case *protocol.CaptureAttachmentDiscardMessage:
		err := d.captureDiscard(m)
		return &protocol.CaptureResult{Discarded: protocol.Ptr(err == nil)}, err
	}
	return nil, fmt.Errorf("unknown capture operation")
}
func (d *Daemon) handleCapture(conn net.Conn, msg any) {
	result, err := d.captureRequest(msg, maxInitialSocketFrameBytes)
	response := protocol.Response{Ok: err == nil, CaptureResult: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrCaptureNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeCaptureNotFound)
		}
	}
	_ = json.NewEncoder(conn).Encode(response)
}
func (d *Daemon) handleCaptureWS(client *wsClient, msg any) {
	if _, download := msg.(*protocol.CaptureAttachmentGetMessage); !download && !client.isTrustedAppClient() {
		d.sendToClient(client, protocol.CaptureResultMessage{Event: protocol.EventCaptureResult, RequestID: captureRequestID(msg), Success: false, Error: protocol.Ptr("capture authoring and history require the authenticated attn app"), ErrorCode: protocol.Ptr(protocol.ErrorCodeUnauthorizedClient)})
		return
	}
	result, err := d.captureRequest(msg, int(websocketReadLimit(client)))
	response := protocol.CaptureResultMessage{Event: protocol.EventCaptureResult, RequestID: captureRequestID(msg), Success: err == nil, Result: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrCaptureNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeCaptureNotFound)
		}
	}
	d.sendToClient(client, response)
}
