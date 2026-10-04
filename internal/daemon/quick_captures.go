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

func (d *Daemon) validateQuickCaptureMailbox(profileID string, mailbox protocol.QuickCaptureMailbox) (inbox.Address, error) {
	switch mailbox.Kind {
	case protocol.QuickCaptureMailboxKindChief:
		if mailbox.MemberID != nil {
			return inbox.Address{}, fmt.Errorf("chief mailbox cannot include member_id")
		}
	case protocol.QuickCaptureMailboxKindCrewMember:
		if strings.TrimSpace(protocol.Deref(mailbox.MemberID)) == "" {
			return inbox.Address{}, fmt.Errorf("crew mailbox requires member_id")
		}
		member, _, err := d.crewMember(*mailbox.MemberID)
		if err != nil {
			return inbox.Address{}, err
		}
		if d.crewProfileID(member.ID) != profileID {
			return inbox.Address{}, fmt.Errorf("crew member %q not found in this profile", *mailbox.MemberID)
		}
		if member.ID != *mailbox.MemberID {
			return inbox.Address{}, fmt.Errorf("member_id must be the stable roster identity %q", member.ID)
		}
	default:
		return inbox.Address{}, fmt.Errorf("unknown quick capture mailbox kind %q", mailbox.Kind)
	}
	if mailbox.Kind == protocol.QuickCaptureMailboxKindChief {
		return inbox.ToChief(profileID), nil
	}
	return inbox.ToMember(*mailbox.MemberID), nil
}

type quickCaptureScope struct {
	profileID       string
	sourceSessionID string
	requestID       string
}

func quickCaptureRequestScope(msg any) quickCaptureScope {
	switch m := msg.(type) {
	case *protocol.QuickCaptureSendMessage:
		return quickCaptureScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.QuickCaptureGetMessage:
		return quickCaptureScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.QuickCaptureListMessage:
		return quickCaptureScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.QuickCaptureAttachmentPutMessage:
		return quickCaptureScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.QuickCaptureAttachmentGetMessage:
		return quickCaptureScope{protocol.Deref(m.ProfileID), protocol.Deref(m.SourceSessionID), protocol.Deref(m.RequestID)}
	case *protocol.QuickCaptureAttachmentDiscardMessage:
		return quickCaptureScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	}
	return quickCaptureScope{}
}
func (d *Daemon) quickCaptureRequest(profileID string, msg any, transportBytes int) (*protocol.QuickCaptureResult, error) {
	if err := d.requireHome("Quick captures"); err != nil {
		return nil, err
	}
	switch m := msg.(type) {
	case *protocol.QuickCaptureSendMessage:
		if err := requireCanonicalUUID(m.CaptureID); err != nil {
			return nil, err
		}
		if m.AttachmentIds == nil {
			m.AttachmentIds = []string{}
		}
		replay, err := d.store.QuickCaptureReplay(profileID, *m)
		if err != nil {
			return nil, err
		}
		if !replay {
			to, err := d.validateQuickCaptureMailbox(profileID, m.Mailbox)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(m.Content) == "" && len(m.AttachmentIds) == 0 {
				return nil, fmt.Errorf("quick capture needs text or at least one file")
			}
			seen := map[string]bool{}
			for _, id := range m.AttachmentIds {
				if err := requireCanonicalUUID(id); err != nil {
					return nil, err
				}
				if seen[id] {
					return nil, fmt.Errorf("attachment %s repeated", id)
				}
				seen[id] = true
			}
			d.quickCaptureAssetMu.Lock()
			err = d.store.SaveQuickCapture(profileID, *m, to, time.Now())
			d.quickCaptureAssetMu.Unlock()
			if err != nil {
				return nil, err
			}
			crashAt("quick-capture-saved")
			d.kickInboxAfterCommit(to)
		}
		r, err := d.store.QuickCapture(profileID, m.CaptureID)
		return &protocol.QuickCaptureResult{Record: r}, err
	case *protocol.QuickCaptureGetMessage:
		r, err := d.store.QuickCapture(profileID, m.CaptureID)
		return &protocol.QuickCaptureResult{Record: r}, err
	case *protocol.QuickCaptureListMessage:
		items, next, err := d.store.QuickCaptures(profileID, m.Limit, protocol.Deref(m.Cursor))
		if err != nil {
			return nil, err
		}
		drafts, err := d.store.QuickCaptureDraftAssets(profileID)
		return &protocol.QuickCaptureResult{List: &protocol.QuickCaptureListResult{Items: items, DraftAssets: drafts, NextCursor: next}}, err
	case *protocol.QuickCaptureAttachmentPutMessage:
		upload, err := d.quickCapturePut(profileID, m)
		return &protocol.QuickCaptureResult{Upload: upload}, err
	case *protocol.QuickCaptureAttachmentGetMessage:
		download, err := d.quickCaptureDownload(profileID, m, transportBytes/2)
		return &protocol.QuickCaptureResult{Download: download}, err
	case *protocol.QuickCaptureAttachmentDiscardMessage:
		err := d.quickCaptureDiscard(profileID, m)
		return &protocol.QuickCaptureResult{Discarded: protocol.Ptr(err == nil)}, err
	}
	return nil, fmt.Errorf("unknown quick capture operation")
}
func (d *Daemon) handleQuickCapture(conn net.Conn, msg any) {
	profileID, err := d.quickCaptureProfile(msg, "")
	var result *protocol.QuickCaptureResult
	if err == nil {
		result, err = d.quickCaptureRequest(profileID, msg, maxInitialSocketFrameBytes)
	}
	response := protocol.Response{Ok: err == nil, QuickCaptureResult: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrQuickCaptureNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeQuickCaptureNotFound)
		}
	}
	_ = json.NewEncoder(conn).Encode(response)
}
func (d *Daemon) handleQuickCaptureWS(client *wsClient, selected string, msg any) {
	if _, download := msg.(*protocol.QuickCaptureAttachmentGetMessage); !download && !client.isTrustedAppClient() {
		d.sendToClient(client, protocol.QuickCaptureResultMessage{ProfileID: selected, Event: protocol.EventQuickCaptureResult, RequestID: quickCaptureRequestScope(msg).requestID, Success: false, Error: protocol.Ptr("quick capture authoring and history require the authenticated attn app"), ErrorCode: protocol.Ptr(protocol.ErrorCodeUnauthorizedClient)})
		return
	}
	profileID, err := d.quickCaptureProfile(msg, selected)
	var result *protocol.QuickCaptureResult
	if err == nil {
		result, err = d.quickCaptureRequest(profileID, msg, int(websocketReadLimit(client)))
	}
	response := protocol.QuickCaptureResultMessage{ProfileID: selected, Event: protocol.EventQuickCaptureResult, RequestID: quickCaptureRequestScope(msg).requestID, Success: err == nil, Result: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrQuickCaptureNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeQuickCaptureNotFound)
		}
	}
	d.sendToClient(client, response)
}

func (d *Daemon) quickCaptureProfile(msg any, selected string) (string, error) {
	scope := quickCaptureRequestScope(msg)
	if selected != "" && scope.profileID != "" && scope.profileID != selected {
		return "", fmt.Errorf("quick capture belongs to profile %s; app selected profile %s", scope.profileID, selected)
	}
	profile, err := d.resolveGardenProfile(scope.sourceSessionID, scope.profileID, selected)
	if err != nil {
		return "", err
	}
	if selected != "" && profile.ID != selected {
		return "", fmt.Errorf("quick capture source session belongs to another profile")
	}
	return profile.ID, nil
}
func (d *Daemon) publishQuickCaptureRead(profileID, captureID, readAt string) {
	d.publishFact(FactQuickCaptureRead, captureID, map[string]string{"profile_id": profileID, "read_at": readAt})
}
