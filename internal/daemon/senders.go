package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type senderPairing struct {
	request protocol.SenderPairingRequest
	allowed chan senderPairingDecision
}

type senderPairingDecision struct {
	allow     bool
	profileID string
}

type senderPairings struct {
	mu      sync.Mutex
	pending *senderPairing
}

func senderTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (d *Daemon) pendingSenderPairing() *protocol.SenderPairingRequest {
	d.senders.mu.Lock()
	defer d.senders.mu.Unlock()
	if d.senders.pending == nil {
		return nil
	}
	request := d.senders.pending.request
	return &request
}

func (d *Daemon) sendersSnapshot(profileID string) protocol.SendersUpdatedMessage {
	senders, err := d.store.PairedSenders(profileID)
	if err != nil {
		d.logf("senders snapshot: %v", err)
		senders = []protocol.PairedSender{}
	}
	return protocol.SendersUpdatedMessage{Event: protocol.EventSendersUpdated, Request: d.pendingSenderPairing(), Senders: senders}
}

func (d *Daemon) projectSenders() {
	if d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotSenders, func() {
		snapshots := map[string]protocol.SendersUpdatedMessage{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profileID := d.profileForClient(client)
			snapshot, ok := snapshots[profileID]
			if !ok {
				snapshot = d.sendersSnapshot(profileID)
				snapshots[profileID] = snapshot
			}
			d.sendToClient(client, snapshot)
		})
	})
}

func (d *Daemon) handleSenderPair(conn net.Conn, msg *protocol.SenderPairMessage, caller protocol.SessionID) {
	if caller != "" {
		d.sendError(conn, fmt.Sprintf("this pair request comes from the environment of attn session %s, and attn refuses those; run attn send pair from the program, outside attn", caller))
		return
	}
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		d.sendError(conn, "sender_pair needs a name")
		return
	}
	code, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	pairing := &senderPairing{
		request: protocol.SenderPairingRequest{ID: uuid.NewString(), Name: name, Code: fmt.Sprintf("%04d", code.Int64()), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		allowed: make(chan senderPairingDecision, 1),
	}
	d.senders.mu.Lock()
	if waiting := d.senders.pending; waiting != nil {
		d.senders.mu.Unlock()
		d.sendError(conn, fmt.Sprintf("%q is already waiting for the user to allow or deny it, and attn asks about one program at a time; ask again after the user answers", waiting.request.Name))
		return
	}
	d.senders.pending = pairing
	d.senders.mu.Unlock()
	defer func() {
		d.senders.mu.Lock()
		if d.senders.pending == pairing {
			d.senders.pending = nil
		}
		d.senders.mu.Unlock()
		d.publishFact(FactSendersChanged, "", nil)
	}()
	d.publishFact(FactSendersChanged, "", nil)
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(protocol.Response{Ok: true, SenderPairing: &pairing.request}); err != nil {
		return
	}
	closed := make(chan struct{})
	goTransport(func() {
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	})
	select {
	case decision := <-pairing.allowed:
		if !decision.allow {
			d.sendError(conn, fmt.Sprintf("the user denied pairing %q", name))
			return
		}
		token := rand.Text() + rand.Text()
		sender := store.PairedSender{PairedSender: protocol.PairedSender{ID: uuid.NewString(), Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}, ProfileID: decision.profileID}
		if err := d.store.AddPairedSender(sender, senderTokenHash(token)); err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if err := encoder.Encode(protocol.Response{Ok: true, PairedSenderToken: &protocol.PairedSenderToken{Sender: sender.PairedSender, Token: token}}); err != nil {
			if _, err := d.store.RemovePairedSender(sender.ProfileID, sender.ID); err != nil {
				d.logf("senders: %q left the pairing before its token arrived, and removing it failed: %v", name, err)
			}
			return
		}
		d.logf("senders: paired %q (%s) in profile %s", name, sender.ID, sender.ProfileID)
	case <-closed:
	case <-d.life.Done():
	}
}

func (d *Daemon) decideSenderPairing(pairingID string, decision senderPairingDecision) error {
	d.senders.mu.Lock()
	defer d.senders.mu.Unlock()
	pairing := d.senders.pending
	if pairing == nil || pairing.request.ID != pairingID {
		return fmt.Errorf("pairing %s is no longer waiting; the program may have stopped asking", pairingID)
	}
	if decision.allow {
		profile, err := d.store.LiveProfile(decision.profileID)
		if err != nil {
			return err
		}
		taken, err := d.store.PairedSenderNameTaken(profile.ID, pairing.request.Name)
		if err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("profile %q already has a sender named %q; remove it from Paired senders, then allow again", profile.Name, pairing.request.Name)
		}
	}
	d.senders.pending = nil
	pairing.allowed <- decision
	return nil
}

func (d *Daemon) handleSenderPairDecide(client *wsClient, msg *protocol.SenderPairDecideMessage) {
	result := protocol.SenderPairDecideResultMessage{Event: protocol.EventSenderPairDecideResult, RequestID: msg.RequestID}
	err := errors.New("only the authenticated attn app can allow or deny a sender")
	if client.isTrustedAppClient() {
		err = d.decideSenderPairing(msg.PairingID, senderPairingDecision{allow: msg.Allow, profileID: d.profileForClient(client)})
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) unpairSender(profile profiles.Profile, idOrName string) error {
	removed, err := d.store.RemovePairedSender(profile.ID, strings.TrimSpace(idOrName))
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("profile %q has no paired sender named or with id %q; attn send senders lists them", profile.Name, idOrName)
	}
	d.logf("senders: unpaired %q from profile %s", idOrName, profile.ID)
	d.publishFact(FactSendersChanged, "", nil)
	return nil
}

func (d *Daemon) handleSenderUnpairWS(client *wsClient, msg *protocol.SenderUnpairMessage) {
	result := protocol.SenderUnpairResultMessage{Event: protocol.EventSenderUnpairResult, RequestID: protocol.Deref(msg.RequestID)}
	profile, err := d.resolveGardenProfile("", protocol.Deref(msg.ProfileID), d.profileForClient(client))
	if err == nil {
		err = d.unpairSender(profile, msg.Sender)
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) requirePairedSender(token string) (*store.PairedSender, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("sending as the user needs a paired sender's token; pair this program once with attn send pair --name <name>, which the user allows in the attn app")
	}
	sender, err := d.store.UsePairedSender(senderTokenHash(strings.TrimSpace(token)), time.Now())
	if err != nil {
		return nil, err
	}
	if sender == nil {
		return nil, errors.New("the sender token matches no paired sender; it was unpaired or never allowed. Pair again with attn send pair --name <name>")
	}
	return sender, nil
}

func (d *Daemon) senderProfile(sender *store.PairedSender, requested string) (string, error) {
	if requested == "" || requested == sender.ProfileID {
		return sender.ProfileID, nil
	}
	return "", fmt.Errorf("sender %q is paired with profile %s and cannot send to profile %s; pair it again from the app with profile %s selected", sender.Name, d.profileLabel(sender.ProfileID), d.profileLabel(requested), d.profileLabel(requested))
}

func (d *Daemon) profileLabel(id string) string {
	if profile, err := d.store.LiveProfile(id); err == nil {
		return fmt.Sprintf("%q (%s)", profile.Name, profile.ID)
	}
	return id
}

func (d *Daemon) handleSenderList(conn net.Conn, msg *protocol.SenderListMessage, caller protocol.SessionID) {
	profile, err := d.resolveGardenProfile(caller, protocol.Deref(msg.ProfileID), "")
	var senders []protocol.PairedSender
	if err == nil {
		senders, err = d.store.PairedSenders(profile.ID)
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, PairedSenders: senders})
}

func (d *Daemon) handleSenderUnpair(conn net.Conn, msg *protocol.SenderUnpairMessage, caller protocol.SessionID) {
	profile, err := d.resolveGardenProfile(caller, protocol.Deref(msg.ProfileID), "")
	if err == nil {
		err = d.unpairSender(profile, msg.Sender)
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handleSenderSessions(conn net.Conn, msg *protocol.SenderSessionsMessage) {
	sender, err := d.requirePairedSender(msg.SenderToken)
	var focused map[protocol.SessionID]string
	if err == nil {
		focused, err = d.store.SessionFocusTimes()
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	sessions := []protocol.SenderSession{}
	for _, session := range d.store.List("") {
		if session.ProfileID != sender.ProfileID {
			continue
		}
		listed := protocol.SenderSession{ID: session.ID, Label: session.Label, Agent: session.Agent, State: session.State, Host: session.Host}
		if at, ok := focused[session.ID]; ok {
			listed.LastFocusedAt = protocol.Ptr(at)
		}
		sessions = append(sessions, listed)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SenderSessions: sessions})
}

func (d *Daemon) noteTerminalFocus(terminal protocol.TerminalID) {
	session := d.terminals().owner(terminal)
	if session == "" {
		return
	}
	if err := d.store.RecordSessionFocus(session, time.Now()); err != nil {
		d.logf("session focus: %s: %v", session, err)
	}
}
