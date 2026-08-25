package store

import (
	"database/sql"
	"time"
)

type TicketDeliveryAttention struct {
	ObserverKey         string
	LastAttentionAt     time.Time
	DeliveredThroughSeq int64
}

func (s *Store) TicketDeliveryAttention(observerKey string) (TicketDeliveryAttention, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || observerKey == "" {
		return TicketDeliveryAttention{}, false, nil
	}
	var raw string
	var deliveredThroughSeq int64
	err := s.db.QueryRow(`SELECT last_attention_at, delivered_through_seq FROM ticket_delivery_attention WHERE observer_key = ?`, observerKey).Scan(&raw, &deliveredThroughSeq)
	if err == sql.ErrNoRows {
		return TicketDeliveryAttention{}, false, nil
	}
	if err != nil {
		return TicketDeliveryAttention{}, false, err
	}
	return TicketDeliveryAttention{ObserverKey: observerKey, LastAttentionAt: parseTicketTime(raw), DeliveredThroughSeq: deliveredThroughSeq}, true, nil
}

func (s *Store) SetTicketDeliveryAttention(observerKey string, at time.Time) error {
	return s.SetTicketDeliveryAttentionThrough(observerKey, at, 0)
}

func (s *Store) SetTicketDeliveryAttentionThrough(observerKey string, at time.Time, deliveredThroughSeq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil || observerKey == "" {
		return nil
	}
	return setTicketDeliveryAttentionTx(s.db, observerKey, at, deliveredThroughSeq)
}

func setTicketDeliveryAttentionTx(ex ticketExecer, observerKey string, at time.Time, deliveredThroughSeq int64) error {
	_, err := ex.Exec(`
		INSERT INTO ticket_delivery_attention (observer_key, last_attention_at, delivered_through_seq)
		VALUES (?, ?, ?)
		ON CONFLICT(observer_key) DO UPDATE SET
			last_attention_at = MAX(last_attention_at, excluded.last_attention_at),
			delivered_through_seq = MAX(delivered_through_seq, excluded.delivered_through_seq)
	`, observerKey, formatTicketTime(at), deliveredThroughSeq)
	return err
}
