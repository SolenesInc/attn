package daemon

import (
	"fmt"
	"sync"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

// clientDocSubscriptions has its own lock: a subscription ends from three directions
// (client asking, client disconnecting, daemon giving up) and none holds the other's.
type clientDocSubscriptions struct {
	mu   sync.Mutex
	subs map[string]chan struct{}
}

func (s *clientDocSubscriptions) open(id string) (done chan struct{}, held int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs == nil {
		s.subs = map[string]chan struct{}{}
	}
	if _, exists := s.subs[id]; exists {
		return nil, len(s.subs), fmt.Errorf("already open")
	}
	if len(s.subs) >= protocol.DocSubscriptionsPerClient {
		return nil, len(s.subs), fmt.Errorf("limit reached")
	}
	done = make(chan struct{})
	s.subs[id] = done
	return done, len(s.subs), nil
}

func (s *clientDocSubscriptions) close(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	done, ok := s.subs[id]
	if !ok {
		return false
	}
	delete(s.subs, id)
	close(done)
	return true
}

// closeAll: a vanished client would otherwise leave loops re-running its queries
// on every write to their collections, forever.
func (s *clientDocSubscriptions) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, done := range s.subs {
		delete(s.subs, id)
		close(done)
	}
}

func (s *clientDocSubscriptions) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (d *Daemon) handleDocSubscribeWS(client *wsClient, msg *protocol.DocSubscribeMessage) {
	id := protocol.Deref(msg.SubscriptionID)
	if id == "" {
		d.sendCommandError(client, protocol.CmdDocSubscribe,
			"doc_subscribe over the WebSocket requires a client-minted subscription_id: one connection carries many subscriptions, so a delivery has to say which one it belongs to")
		return
	}
	q, err := docSubscriptionQuery(msg)
	if err != nil {
		d.endDocSubscriptionWS(client, id, err, docErrorCode(err))
		return
	}

	done, held, err := client.docSubscriptions.open(id)
	if err != nil {
		if held >= protocol.DocSubscriptionsPerClient {
			d.endDocSubscriptionWS(client, id, fmt.Errorf(
				"docstore: this client already holds %d live subscriptions, which is the limit (%d), so %q was refused. Unsubscribe what is no longer on screen; a tile that unmounts must send doc_unsubscribe",
				held, protocol.DocSubscriptionsPerClient, id), protocol.ErrorCodeSubscriptionLimit)
			return
		}
		d.endDocSubscriptionWS(client, id, docstore.InvalidQuery(fmt.Errorf(
			"docstore: subscription_id %q is already open on this connection; mint a fresh id per subscribe, or doc_unsubscribe the first one",
			id)), protocol.ErrorCodeInvalidQuery)
		return
	}

	// Its own goroutine: the loop blocks between deliveries, and the pump that got us here
	// handles this client's commands in order — including the doc_unsubscribe that ends it.
	go func() {
		defer client.docSubscriptions.close(id)
		d.runDocSubscription(q, msg.Have, docSink{
			deliver: func(window *protocol.DocSubscribeResult) error {
				if !d.sendToClient(client, protocol.DocSubscriptionDeliveryMessage{
					Event:          protocol.EventDocSubscriptionDelivery,
					SubscriptionID: id,
					Delivery:       window.Delivery,
					AsOfSeq:        window.AsOfSeq,
					Order:          window.Order,
					Upsert:         window.Upsert,
				}) {
					return fmt.Errorf("client gone")
				}
				return nil
			},
			end: func(err error, code string) { d.endDocSubscriptionWS(client, id, err, code) },
		}, done)
	}()
}

func (d *Daemon) handleDocUnsubscribeWS(client *wsClient, msg *protocol.DocUnsubscribeMessage) {
	client.docSubscriptions.close(msg.SubscriptionID)
}

func (d *Daemon) endDocSubscriptionWS(client *wsClient, id string, err error, code string) {
	if code == "" {
		code = protocol.ErrorCodeInvalidQuery
	}
	d.sendToClient(client, protocol.DocSubscriptionEndedMessage{
		Event:          protocol.EventDocSubscriptionEnded,
		SubscriptionID: id,
		Code:           code,
		Error:          err.Error(),
	})
}

func (d *Daemon) dropDocSubscriptions(client *wsClient) {
	if client == nil {
		return
	}
	client.docSubscriptions.closeAll()
}
