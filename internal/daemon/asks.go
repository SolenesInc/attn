package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) asksSettingOn() bool {
	return parseBooleanSetting(d.store.GetSetting(SettingAsksEnabled))
}

func (d *Daemon) launchesWithAsks(sessionID protocol.SessionID) bool {
	if intent, ok := d.store.LaunchIntent(sessionID); ok {
		return intent.Asks
	}
	return d.asksSettingOn()
}

func (d *Daemon) handleAskOpen(conn net.Conn, msg *protocol.AskOpenMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	if err := d.askingSession(sessionID); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	parts, err := ask.Parse(msg.Markdown)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	id, err := ask.NewID()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	stored := store.Ask{
		ID:        id,
		SessionID: sessionID,
		To:        d.inboxAddressOf(sessionID),
		Subject:   ask.Subject(protocol.Deref(msg.Subject), parts),
		Markdown:  msg.Markdown,
		BaseDir:   strings.TrimSpace(msg.BaseDir),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := d.store.AddAsk(stored); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	opened := askToProto(stored, parts)
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskOpenResult: &opened})
	d.publishFact(FactAskOpened, id, nil)
}

func (d *Daemon) askingSession(sessionID protocol.SessionID) error {
	if sessionID == "" || d.store.Get(sessionID) == nil {
		return fmt.Errorf("session %q is not live in attn", sessionID)
	}
	if !d.launchesWithAsks(sessionID) {
		return errors.New("asks are off for this session because it launched without them; ask in the conversation instead")
	}
	return nil
}

func (d *Daemon) handleAskList(conn net.Conn, msg *protocol.AskListMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	if d.store.Get(sessionID) == nil {
		d.sendError(conn, fmt.Sprintf("session %q is not live in attn", sessionID))
		return
	}
	result := &protocol.AskListResult{Enabled: d.launchesWithAsks(sessionID), Asks: []protocol.Ask{}}
	to := d.inboxAddressOf(sessionID)
	for _, open := range d.openAsks() {
		if open.stored.To == to {
			result.Asks = append(result.Asks, open.wire)
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskListResult: result})
}

func (d *Daemon) handleAskClose(conn net.Conn, msg *protocol.AskCloseMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	stored, err := d.store.GetOpenAsk(strings.TrimSpace(msg.AskID))
	if err != nil || stored.To != d.inboxAddressOf(sessionID) {
		d.sendError(conn, fmt.Sprintf("%s is not an open ask of this session", msg.AskID))
		return
	}
	if err := d.store.CloseAsk(stored.ID, store.AskWithdrawn, time.Now()); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true})
	d.publishFact(FactAskClosed, stored.ID, nil)
}

func (d *Daemon) handleAskAnswer(client *wsClient, msg *protocol.AskAnswerMessage) {
	result := protocol.AskAnswerResultMessage{Event: protocol.EventAskAnswerResult, RequestID: msg.RequestID, AskID: msg.AskID}
	if err := d.answerAsk(msg); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) answerAsk(msg *protocol.AskAnswerMessage) error {
	stored, err := d.store.GetOpenAsk(strings.TrimSpace(msg.AskID))
	if errors.Is(err, store.ErrAskNotOpen) {
		return errors.New("this ask was already answered or withdrawn")
	}
	if err != nil {
		return err
	}
	parts, err := ask.Parse(stored.Markdown)
	if err != nil {
		return err
	}
	text, err := ask.Reply(askToProto(stored, parts), msg.Answers, protocol.Deref(msg.Note))
	if err != nil {
		return err
	}
	if _, err := d.sendToInbox(inbox.Item{ID: "ask-answer/" + stored.ID, To: stored.To, Kind: inbox.Notice, Source: stored.ID, Text: text}); err != nil {
		return fmt.Errorf("deliver the answer: %w", err)
	}
	if err := d.store.CloseAsk(stored.ID, store.AskAnswered, time.Now()); err != nil {
		return errors.New("this ask was already answered or withdrawn")
	}
	d.publishFact(FactAskClosed, stored.ID, nil)
	if holder := d.inboxHolder(stored.To); holder != nil && holder.State != protocol.SessionStatePendingApproval {
		d.handleSettleTurn(&protocol.SettleTurnMessage{SessionID: holder.ID})
	}
	return nil
}

func (d *Daemon) subscribeAskFacts() {
	d.asksUnsubscribe = d.eventBus.Subscribe(bus.Filter{FactSessionClosed, FactSessionUnregistered}, func(ev bus.Event) {
		d.life.Go("asks-session-closed", func() {
			ids, err := d.store.CloseAsksTo(inbox.ToSession(protocol.SessionID(ev.Subject)), store.AskSessionClosed, time.Now())
			if err != nil {
				d.logf("asks: closing the asks of session %s: %v", ev.Subject, err)
			}
			for _, id := range ids {
				d.publishFact(FactAskClosed, id, nil)
			}
		})
	})
}

type openAsk struct {
	stored    store.Ask
	wire      protocol.Ask
	profileID string
}

func (d *Daemon) openAsks() []openAsk {
	stored, err := d.store.OpenAsks()
	if err != nil {
		d.logf("asks: reading open asks: %v", err)
		return nil
	}
	asks := make([]openAsk, 0, len(stored))
	for _, a := range stored {
		parts, err := ask.Parse(a.Markdown)
		if err != nil {
			d.logf("asks: ask %s no longer parses: %v", a.ID, err)
			continue
		}
		profileID, _ := d.store.SessionProfileID(a.SessionID)
		asks = append(asks, openAsk{stored: a, wire: askToProto(a, parts), profileID: profileID})
	}
	return asks
}

func (d *Daemon) asksForProfile(profileID string) []protocol.Ask {
	asks := []protocol.Ask{}
	for _, open := range d.openAsks() {
		if open.profileID == profileID {
			asks = append(asks, open.wire)
		}
	}
	return asks
}

func (d *Daemon) projectAsks() {
	if d.store == nil || d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotAsks, func() {
		open := d.openAsks()
		snapshots := map[string][]byte{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profileID := client.selectedProfile()
			data, ok := snapshots[profileID]
			if !ok {
				var err error
				data, err = json.Marshal(asksSnapshot(profileID, open))
				if err != nil {
					d.logf("asks snapshot: %v", err)
					return
				}
				snapshots[profileID] = data
			}
			d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
		})
	})
}

func asksSnapshot(profileID string, open []openAsk) *protocol.AsksUpdatedMessage {
	message := &protocol.AsksUpdatedMessage{Event: protocol.EventAsksUpdated, ProfileID: profileID, Asks: []protocol.Ask{}}
	for _, a := range open {
		if profileID != "" && a.profileID == profileID {
			message.Asks = append(message.Asks, a.wire)
		}
	}
	return message
}

func askToProto(a store.Ask, parts []protocol.AskPart) protocol.Ask {
	wire := protocol.Ask{
		ID:        a.ID,
		SessionID: a.SessionID,
		Subject:   a.Subject,
		Parts:     parts,
		BaseDir:   a.BaseDir,
		CreatedAt: a.CreatedAt,
	}
	if member := a.To.MemberID(); member != "" {
		wire.CrewMember = protocol.Ptr(member)
	}
	if a.To.ChiefProfileID() != "" {
		wire.Chief = protocol.Ptr(true)
	}
	return wire
}
