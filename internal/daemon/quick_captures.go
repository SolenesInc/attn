package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) validateQuickCaptureMailbox(profileID string, mailbox protocol.QuickCaptureMailbox) (who.Address, error) {
	if mailbox.Kind != protocol.QuickCaptureMailboxKindSession && mailbox.SessionID != nil {
		return who.Address{}, fmt.Errorf("%s mailbox cannot include session_id", mailbox.Kind)
	}
	switch mailbox.Kind {
	case protocol.QuickCaptureMailboxKindSession:
		if mailbox.MemberID != nil {
			return who.Address{}, fmt.Errorf("session mailbox cannot include member_id")
		}
		id := protocol.TrimID(protocol.Deref(mailbox.SessionID))
		if id == "" {
			return who.Address{}, fmt.Errorf("session mailbox requires session_id")
		}
		session := d.store.Get(id)
		if session == nil {
			return who.Address{}, fmt.Errorf("session %s is not running", id)
		}
		if session.ProfileID != profileID {
			return who.Address{}, fmt.Errorf("session %s belongs to another profile", id)
		}
		if !sessionReadsInboxDoorbells(session) {
			return who.Address{}, fmt.Errorf("session %s is a shell; a capture goes only to an agent session", id)
		}
		return d.inboxAddressOf(session.ID), nil
	case protocol.QuickCaptureMailboxKindCrewMember:
		if strings.TrimSpace(protocol.Deref(mailbox.MemberID)) == "" {
			return who.Address{}, fmt.Errorf("crew mailbox requires member_id")
		}
		member, _, err := d.crewMember(*mailbox.MemberID)
		if err != nil {
			return who.Address{}, err
		}
		if member.ProfileID != profileID {
			return who.Address{}, fmt.Errorf("crew member %q not found in this profile", *mailbox.MemberID)
		}
		if member.ID != *mailbox.MemberID {
			return who.Address{}, fmt.Errorf("member_id must be the stable roster identity %q", member.ID)
		}
		return who.Member(member.Key).Address(), nil
	}
	return who.Address{}, fmt.Errorf("unknown quick capture mailbox kind %q", mailbox.Kind)
}

type requestScope struct {
	profileID       string
	sourceSessionID protocol.SessionID
	requestID       string
}

func scopeOf(msg any) requestScope {
	switch m := msg.(type) {
	case *protocol.QuickCaptureSendMessage:
		return requestScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.QuickCaptureGetMessage:
		return requestScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.QuickCaptureListMessage:
		return requestScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.AttachmentPutMessage:
		return requestScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	case *protocol.AttachmentGetMessage:
		return requestScope{protocol.Deref(m.ProfileID), protocol.Deref(m.SourceSessionID), protocol.Deref(m.RequestID)}
	case *protocol.AttachmentDiscardMessage:
		return requestScope{protocol.Deref(m.ProfileID), "", protocol.Deref(m.RequestID)}
	}
	return requestScope{}
}

const socketDownloadBytes = 32 * 1024

func (d *Daemon) quickCaptureRequest(profileID string, msg any) (*protocol.QuickCaptureResult, error) {
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
			d.attachmentMu.Lock()
			err = d.store.SaveQuickCapture(profileID, *m, to, time.Now())
			d.attachmentMu.Unlock()
			if err != nil {
				return nil, err
			}
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
	}
	return nil, fmt.Errorf("unknown quick capture operation")
}

func (d *Daemon) attachmentRequest(profileID string, msg any, downloadChunkBytes int) (*protocol.AttachmentResult, error) {
	switch m := msg.(type) {
	case *protocol.AttachmentPutMessage:
		upload, err := d.attachmentPut(profileID, m)
		return &protocol.AttachmentResult{Upload: upload}, err
	case *protocol.AttachmentGetMessage:
		download, err := d.attachmentDownload(profileID, m, downloadChunkBytes)
		return &protocol.AttachmentResult{Download: download}, err
	case *protocol.AttachmentDiscardMessage:
		err := d.attachmentDiscard(profileID, m)
		return &protocol.AttachmentResult{Discarded: protocol.Ptr(err == nil)}, err
	}
	return nil, fmt.Errorf("unknown attachment operation")
}

func senderTokenOf(msg any) (string, bool) {
	switch m := msg.(type) {
	case *protocol.QuickCaptureSendMessage:
		return protocol.Deref(m.SenderToken), true
	case *protocol.AttachmentPutMessage:
		return protocol.Deref(m.SenderToken), true
	case *protocol.AttachmentDiscardMessage:
		return protocol.Deref(m.SenderToken), true
	}
	return "", false
}

func (d *Daemon) socketRequestProfile(msg any) (string, error) {
	token, authoring := senderTokenOf(msg)
	if !authoring {
		return d.requestProfile(msg, "")
	}
	sender, err := d.requirePairedSender(token)
	if err != nil {
		return "", err
	}
	return d.senderProfile(sender, scopeOf(msg).profileID)
}

func refusal(err error) (*string, *string) {
	if err == nil {
		return nil, nil
	}
	if errors.Is(err, store.ErrQuickCaptureNotFound) {
		return protocol.Ptr(err.Error()), protocol.Ptr(protocol.ErrorCodeQuickCaptureNotFound)
	}
	return protocol.Ptr(err.Error()), nil
}

func (d *Daemon) handleQuickCapture(conn net.Conn, msg any) {
	profileID, err := d.socketRequestProfile(msg)
	var result *protocol.QuickCaptureResult
	if err == nil {
		result, err = d.quickCaptureRequest(profileID, msg)
	}
	response := protocol.Response{Ok: err == nil, QuickCaptureResult: result}
	response.Error, response.ErrorCode = refusal(err)
	_ = json.NewEncoder(conn).Encode(response)
}

func (d *Daemon) handleAttachment(conn net.Conn, msg any) {
	profileID, err := d.socketRequestProfile(msg)
	var result *protocol.AttachmentResult
	if err == nil {
		result, err = d.attachmentRequest(profileID, msg, socketDownloadBytes)
	}
	response := protocol.Response{Ok: err == nil, AttachmentResult: result}
	response.Error, response.ErrorCode = refusal(err)
	_ = json.NewEncoder(conn).Encode(response)
}

func (d *Daemon) readAttachment(_ context.Context, msg protocol.AttachmentGetMessage) (protocol.AttachmentResult, error) {
	profileID, err := d.requestProfile(&msg, "")
	if err != nil {
		return protocol.AttachmentResult{}, err
	}
	result, err := d.attachmentRequest(profileID, &msg, socketDownloadBytes)
	if err != nil {
		return protocol.AttachmentResult{}, err
	}
	return *result, nil
}

const untrustedClientRefusal = "quick capture authoring, history and file uploads require the authenticated attn app"

func (d *Daemon) handleQuickCaptureWS(client *wsClient, selected string, msg any) {
	response := protocol.QuickCaptureResultMessage{ProfileID: selected, Event: protocol.EventQuickCaptureResult, RequestID: scopeOf(msg).requestID}
	if !client.isTrustedAppClient() {
		response.Error, response.ErrorCode = protocol.Ptr(untrustedClientRefusal), protocol.Ptr(protocol.ErrorCodeUnauthorizedClient)
		d.sendToClient(client, response)
		return
	}
	profileID, err := d.requestProfile(msg, selected)
	if err == nil {
		response.Result, err = d.quickCaptureRequest(profileID, msg)
	}
	response.Success = err == nil
	response.Error, response.ErrorCode = refusal(err)
	d.sendToClient(client, response)
}

func (d *Daemon) handleAttachmentWS(client *wsClient, selected string, msg any) {
	response := protocol.AttachmentResultMessage{ProfileID: selected, Event: protocol.EventAttachmentResult, RequestID: scopeOf(msg).requestID}
	if _, download := msg.(*protocol.AttachmentGetMessage); !download && !client.isTrustedAppClient() {
		response.Error, response.ErrorCode = protocol.Ptr(untrustedClientRefusal), protocol.Ptr(protocol.ErrorCodeUnauthorizedClient)
		d.sendToClient(client, response)
		return
	}
	profileID, err := d.requestProfile(msg, selected)
	if err == nil {
		response.Result, err = d.attachmentRequest(profileID, msg, int(websocketReadLimit(client))/2)
	}
	response.Success = err == nil
	response.Error, response.ErrorCode = refusal(err)
	d.sendToClient(client, response)
}

func (d *Daemon) requestProfile(msg any, selected string) (string, error) {
	scope := scopeOf(msg)
	if selected != "" && scope.profileID != "" && scope.profileID != selected {
		return "", fmt.Errorf("the request names profile %s; app selected profile %s", scope.profileID, selected)
	}
	profile, err := d.resolveGardenProfile(scope.sourceSessionID, scope.profileID, selected)
	if err != nil {
		return "", err
	}
	if selected != "" && profile.ID != selected {
		return "", fmt.Errorf("the source session belongs to another profile")
	}
	return profile.ID, nil
}
func (d *Daemon) publishQuickCaptureRead(profileID, captureID, readAt string) {
	d.publishFact(FactQuickCaptureRead, captureID, map[string]string{"profile_id": profileID, "read_at": readAt})
}
