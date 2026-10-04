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

const FactUserMessageChanged = "user_message.changed"

func (d *Daemon) validateUserMessageTarget(profileID string, target protocol.UserMessageTarget) (inbox.Address, error) {
	switch target.Kind {
	case protocol.UserMessageTargetKindChief:
		if target.MemberID != nil {
			return inbox.Address{}, fmt.Errorf("chief target cannot include member_id")
		}
	case protocol.UserMessageTargetKindCrew:
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
		return inbox.Address{}, fmt.Errorf("unknown user message target kind %q", target.Kind)
	}
	if target.Kind == protocol.UserMessageTargetKindChief {
		return inbox.ToChief(profileID), nil
	}
	return inbox.ToMember(*target.MemberID), nil
}
func userMessageRequestID(msg any) string {
	switch m := msg.(type) {
	case *protocol.UserMessageSendMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.UserMessageGetMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.UserMessageListMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.UserMessageAttachmentPutMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.UserMessageAttachmentGetMessage:
		return protocol.Deref(m.RequestID)
	case *protocol.UserMessageAttachmentDiscardMessage:
		return protocol.Deref(m.RequestID)
	}
	return ""
}
func (d *Daemon) userMessageRequest(profileID string, msg any, transportBytes int) (*protocol.UserMessageResult, error) {
	if err := d.requireHome("User messages"); err != nil {
		return nil, err
	}
	switch m := msg.(type) {
	case *protocol.UserMessageSendMessage:
		if err := messageID(m.MessageID); err != nil {
			return nil, err
		}
		if m.AttachmentIds == nil {
			m.AttachmentIds = []string{}
		}
		replay, err := d.store.UserMessageReplay(profileID, *m)
		if err != nil {
			return nil, err
		}
		if !replay {
			to, err := d.validateUserMessageTarget(profileID, m.Target)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(m.Content) == "" && len(m.AttachmentIds) == 0 {
				return nil, fmt.Errorf("user message needs text or at least one file")
			}
			seen := map[string]bool{}
			for _, id := range m.AttachmentIds {
				if err := messageID(id); err != nil {
					return nil, err
				}
				if seen[id] {
					return nil, fmt.Errorf("attachment %s repeated", id)
				}
				seen[id] = true
			}
			d.userMessageAssetMu.Lock()
			err = d.store.SaveUserMessage(profileID, *m, to, time.Now())
			d.userMessageAssetMu.Unlock()
			if err != nil {
				return nil, err
			}
			crashAt("user-message-saved")
			d.publishUserMessageChanged(profileID, m.MessageID)
			d.kickInboxAfterCommit(to)
		}
		r, err := d.store.UserMessage(profileID, m.MessageID)
		return &protocol.UserMessageResult{Record: r}, err
	case *protocol.UserMessageGetMessage:
		r, err := d.store.UserMessage(profileID, m.MessageID)
		return &protocol.UserMessageResult{Record: r}, err
	case *protocol.UserMessageListMessage:
		items, next, err := d.store.UserMessages(profileID, m.Limit, protocol.Deref(m.Cursor))
		if err != nil {
			return nil, err
		}
		drafts, err := d.store.UserMessageDraftAssets(profileID)
		return &protocol.UserMessageResult{List: &protocol.UserMessageListResult{Items: items, DraftAssets: drafts, NextCursor: next}}, err
	case *protocol.UserMessageAttachmentPutMessage:
		upload, err := d.userMessagePut(profileID, m)
		return &protocol.UserMessageResult{Upload: upload}, err
	case *protocol.UserMessageAttachmentGetMessage:
		download, err := d.userMessageDownload(profileID, m, transportBytes/2)
		return &protocol.UserMessageResult{Download: download}, err
	case *protocol.UserMessageAttachmentDiscardMessage:
		err := d.userMessageDiscard(profileID, m)
		return &protocol.UserMessageResult{Discarded: protocol.Ptr(err == nil)}, err
	}
	return nil, fmt.Errorf("unknown user message operation")
}
func (d *Daemon) handleUserMessage(conn net.Conn, msg any) {
	profileID, err := d.userMessageProfile(msg, "")
	var result *protocol.UserMessageResult
	if err == nil {
		result, err = d.userMessageRequest(profileID, msg, maxInitialSocketFrameBytes)
	}
	response := protocol.Response{Ok: err == nil, UserMessageResult: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrUserMessageNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeUserMessageNotFound)
		}
	}
	_ = json.NewEncoder(conn).Encode(response)
}
func (d *Daemon) handleUserMessageWS(client *wsClient, selected string, msg any) {
	if _, download := msg.(*protocol.UserMessageAttachmentGetMessage); !download && !client.isTrustedAppClient() {
		d.sendToClient(client, protocol.UserMessageResultMessage{ProfileID: selected, Event: protocol.EventUserMessageResult, RequestID: userMessageRequestID(msg), Success: false, Error: protocol.Ptr("user message authoring and history require the authenticated attn app"), ErrorCode: protocol.Ptr(protocol.ErrorCodeUnauthorizedClient)})
		return
	}
	profileID, err := d.userMessageProfile(msg, selected)
	var result *protocol.UserMessageResult
	if err == nil {
		result, err = d.userMessageRequest(profileID, msg, int(websocketReadLimit(client)))
	}
	response := protocol.UserMessageResultMessage{ProfileID: selected, Event: protocol.EventUserMessageResult, RequestID: userMessageRequestID(msg), Success: err == nil, Result: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		if errors.Is(err, store.ErrUserMessageNotFound) {
			response.ErrorCode = protocol.Ptr(protocol.ErrorCodeUserMessageNotFound)
		}
	}
	d.sendToClient(client, response)
}

func (d *Daemon) userMessageProfile(msg any, selected string) (string, error) {
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
		return "", fmt.Errorf("user message belongs to profile %s; app selected profile %s", scope.ProfileID, selected)
	}
	profile, err := d.resolveGardenProfile(scope.SourceSessionID, scope.ProfileID, selected)
	if err != nil {
		return "", err
	}
	if selected != "" && profile.ID != selected {
		return "", fmt.Errorf("user message source session belongs to another profile")
	}
	return profile.ID, nil
}
func (d *Daemon) publishUserMessageChanged(profileID, messageID string) {
	d.publishFact(FactUserMessageChanged, messageID, map[string]string{"profile_id": profileID})
}
