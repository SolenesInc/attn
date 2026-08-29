package ticketnotify

import (
	"sort"
	"time"

	"github.com/victorarias/attn/internal/store"
)

type EventStore interface {
	UnreadTicketEventsFor(cursorIdentity, authorIdentity string) ([]store.TicketEvent, error)
	SetTicketCursor(identity, ticketID string, cursor int64, now time.Time) error
}

type Observer struct {
	ID         string
	AuthorID   string
	DeliveryID string
}

type Bundle struct {
	TicketID string
	Events   []store.TicketEvent
}

// Not atomic: two Consumes racing for the SAME observer — or a crash
// mid-ConsumeAll — double-deliver, never lose.
func Consume(es EventStore, obs Observer, now time.Time) ([]Bundle, error) {
	bundles, advance, err := pending(es, obs)
	if err != nil {
		return nil, err
	}
	for ticketID, seq := range advance {
		if err := es.SetTicketCursor(obs.ID, ticketID, seq, now); err != nil {
			return nil, err
		}
	}
	return bundles, nil
}

func ConsumeAll(es EventStore, observers []Observer, now time.Time) ([]Bundle, error) {
	byTicket := map[string]map[int64]store.TicketEvent{}
	for _, obs := range observers {
		bundles, err := Consume(es, obs, now)
		if err != nil {
			return nil, err
		}
		for _, bundle := range bundles {
			if byTicket[bundle.TicketID] == nil {
				byTicket[bundle.TicketID] = map[int64]store.TicketEvent{}
			}
			for _, event := range bundle.Events {
				byTicket[bundle.TicketID][event.Seq] = event
			}
		}
	}
	merged := make([]Bundle, 0, len(byTicket))
	for ticketID, events := range byTicket {
		bundle := Bundle{TicketID: ticketID, Events: make([]store.TicketEvent, 0, len(events))}
		for _, event := range events {
			bundle.Events = append(bundle.Events, event)
		}
		sort.Slice(bundle.Events, func(i, j int) bool { return bundle.Events[i].Seq < bundle.Events[j].Seq })
		merged = append(merged, bundle)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Events[0].Seq < merged[j].Events[0].Seq })
	return merged, nil
}

func Unread(es EventStore, obs Observer) (int, error) {
	bundles, _, err := pending(es, obs)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range bundles {
		n += len(b.Events)
	}
	return n, nil
}

func UnreadAny(es EventStore, observers []Observer) (int, error) {
	hasUnread := false
	for _, obs := range observers {
		n, err := Unread(es, obs)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			hasUnread = true
		}
	}
	if hasUnread {
		return 1, nil
	}
	return 0, nil
}

type Delivery int

const (
	DeliveryNone Delivery = iota
	DeliveryNudge
	DeliveryDeferred
)

// Carries NO event content — only the bounded "go consume your tickets"
// trigger, mirroring the daemon's doorbell rule.
type Nudger interface {
	Nudge(observerID string) error
}

func Notify(es EventStore, obs Observer, nudgeEligible bool, nudger Nudger, now time.Time) (Delivery, error) {
	return NotifyAny(es, []Observer{obs}, obs, nudgeEligible, nudger, now)
}

func NotifyAny(es EventStore, observers []Observer, deliveryObserver Observer, nudgeEligible bool, nudger Nudger, now time.Time) (Delivery, error) {
	unread, err := UnreadAny(es, observers)
	if err != nil {
		return DeliveryNone, err
	}
	if unread == 0 {
		return DeliveryNone, nil
	}
	if !nudgeEligible {
		return DeliveryDeferred, nil
	}
	deliveryID := deliveryObserver.DeliveryID
	if deliveryID == "" {
		deliveryID = deliveryObserver.ID
	}
	if err := nudger.Nudge(deliveryID); err != nil {
		return DeliveryNone, err
	}
	return DeliveryNudge, nil
}

func pending(es EventStore, obs Observer) (bundles []Bundle, advance map[string]int64, err error) {
	authorID := obs.AuthorID
	if authorID == "" {
		authorID = obs.ID
	}
	events, err := es.UnreadTicketEventsFor(obs.ID, authorID)
	if err != nil {
		return nil, nil, err
	}
	advance = map[string]int64{}
	index := map[string]int{}
	for _, e := range events {
		i, ok := index[e.TicketID]
		if !ok {
			i = len(bundles)
			index[e.TicketID] = i
			bundles = append(bundles, Bundle{TicketID: e.TicketID})
		}
		bundles[i].Events = append(bundles[i].Events, e)
		if e.Seq > advance[e.TicketID] {
			advance[e.TicketID] = e.Seq
		}
	}
	sort.SliceStable(bundles, func(i, j int) bool {
		return bundles[i].Events[0].Seq < bundles[j].Events[0].Seq
	})
	return bundles, advance, nil
}
