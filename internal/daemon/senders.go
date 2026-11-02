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
	"github.com/victorarias/attn/internal/protocol"
)

type senderPairing struct {
	request protocol.SenderPairingRequest
	allowed chan bool
}

type senderPairings struct {
	mu      sync.Mutex
	pending *senderPairing
}

func senderTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (d *Daemon) pendingSenderPairings() []protocol.SenderPairingRequest {
	d.senders.mu.Lock()
	defer d.senders.mu.Unlock()
	if d.senders.pending == nil {
		return []protocol.SenderPairingRequest{}
	}
	return []protocol.SenderPairingRequest{d.senders.pending.request}
}

func (d *Daemon) sendersSnapshot() protocol.SendersUpdatedMessage {
	senders, err := d.store.PairedSenders()
	if err != nil {
		d.logf("senders snapshot: %v", err)
		senders = []protocol.PairedSender{}
	}
	return protocol.SendersUpdatedMessage{Event: protocol.EventSendersUpdated, Requests: d.pendingSenderPairings(), Senders: senders}
}

func (d *Daemon) projectSenders() {
	if d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotSenders, func() {
		d.wsHub.BroadcastValue(d.sendersSnapshot())
	})
}

func (d *Daemon) handleSenderPair(conn net.Conn, msg *protocol.SenderPairMessage, caller protocol.SessionID) {
	if caller != "" {
		d.sendError(conn, fmt.Sprintf("session %s runs inside attn, and agents cannot pair senders; run attn send pair from the program outside attn", caller))
		return
	}
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		d.sendError(conn, "sender_pair needs a name")
		return
	}
	if taken, err := d.store.PairedSenderNameTaken(name); err != nil || taken {
		if err == nil {
			err = fmt.Errorf("a sender named %q is already paired; remove it with attn send unpair %q, then pair again", name, name)
		}
		d.sendError(conn, err.Error())
		return
	}
	code, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	pairing := &senderPairing{
		request: protocol.SenderPairingRequest{ID: uuid.NewString(), Name: name, Code: fmt.Sprintf("%04d", code.Int64()), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		allowed: make(chan bool, 1),
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
	case allowed := <-pairing.allowed:
		if !allowed {
			d.sendError(conn, fmt.Sprintf("the user denied pairing %q", name))
			return
		}
		token := rand.Text() + rand.Text()
		sender := protocol.PairedSender{ID: uuid.NewString(), Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		if err := d.store.AddPairedSender(sender, senderTokenHash(token)); err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if err := encoder.Encode(protocol.Response{Ok: true, PairedSenderToken: &protocol.PairedSenderToken{Sender: sender, Token: token}}); err != nil {
			if _, err := d.store.RemovePairedSender(sender.ID); err != nil {
				d.logf("senders: %q left the pairing before its token arrived, and removing it failed: %v", name, err)
			}
			return
		}
		d.logf("senders: paired %q (%s)", name, sender.ID)
	case <-closed:
	case <-d.life.Done():
	}
}

func (d *Daemon) decideSenderPairing(pairingID string, allow bool) error {
	d.senders.mu.Lock()
	pairing := d.senders.pending
	if pairing != nil && pairing.request.ID == pairingID {
		d.senders.pending = nil
	} else {
		pairing = nil
	}
	d.senders.mu.Unlock()
	if pairing == nil {
		return fmt.Errorf("pairing %s is no longer waiting; the program may have stopped asking", pairingID)
	}
	pairing.allowed <- allow
	return nil
}

func (d *Daemon) handleSenderPairDecide(client *wsClient, msg *protocol.SenderPairDecideMessage) {
	result := protocol.SenderPairDecideResultMessage{Event: protocol.EventSenderPairDecideResult, RequestID: msg.RequestID}
	err := errors.New("only the authenticated attn app can allow or deny a sender")
	if client.isTrustedAppClient() {
		err = d.decideSenderPairing(msg.PairingID, msg.Allow)
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) unpairSender(idOrName string) error {
	removed, err := d.store.RemovePairedSender(strings.TrimSpace(idOrName))
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no paired sender is named or has id %q; attn send senders lists them", idOrName)
	}
	d.logf("senders: unpaired %q", idOrName)
	d.publishFact(FactSendersChanged, "", nil)
	return nil
}

func (d *Daemon) handleSenderUnpairWS(client *wsClient, msg *protocol.SenderUnpairMessage) {
	result := protocol.SenderUnpairResultMessage{Event: protocol.EventSenderUnpairResult, RequestID: protocol.Deref(msg.RequestID)}
	if err := d.unpairSender(msg.Sender); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) requirePairedSender(token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("sending as the user needs a paired sender's token; pair this program once with attn send pair --name <name>, which the user allows in the attn app")
	}
	sender, err := d.store.UsePairedSender(senderTokenHash(strings.TrimSpace(token)), time.Now())
	if err != nil {
		return err
	}
	if sender == nil {
		return errors.New("the sender token matches no paired sender; it was unpaired or never allowed. Pair again with attn send pair --name <name>")
	}
	return nil
}
