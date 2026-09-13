package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/store"
)

const gardenSeedBellConsumer = "garden-seed-bells"

var gardenSeedEventModel, gardenSeedEventVocabulary = mustBuildGardenSeedEvents()

func mustBuildGardenSeedEvents() (*events.Model, events.Vocabulary) {
	model, vocabulary, err := events.BuildGardenModel()
	if err != nil {
		panic(err)
	}
	return model, vocabulary
}

func encodeGardenSeedEvents(occurrences ...events.Occurrence) ([]store.BusEvent, error) {
	encoded := make([]store.BusEvent, len(occurrences))
	for i, occurrence := range occurrences {
		event, err := gardenSeedEventModel.Encode(occurrence)
		if err != nil {
			return nil, err
		}
		encoded[i] = store.BusEvent{
			Name: event.Name, Subject: event.Subject, Payload: string(event.Payload), Source: "garden",
		}
	}
	return encoded, nil
}

func announceGardenSeedEvents(d *Daemon, seqs []int64) {
	if len(seqs) == 0 || d == nil {
		return
	}
	d.coalesceSnapshots(func() {
		if d.eventBus != nil {
			d.eventBus.Announce()
		}
		if d.gardenSeedEventConsumerStarted || d.store == nil {
			return
		}
		for _, seq := range seqs {
			rows, err := d.store.BusEventsSince(seq-1, 1)
			if err != nil || len(rows) != 1 || rows[0].Seq != seq {
				d.logf("Garden seed event %d committed but test-mode handling could not read it: rows=%d err=%v", seq, len(rows), err)
				continue
			}
			row := rows[0]
			if err := d.handleGardenSeedEventWithoutRoleLock(context.Background(), bus.Event{
				Seq: row.Seq, Name: row.Name, Subject: row.Subject, Payload: []byte(row.Payload),
				Source: row.Source, CreatedAt: row.CreatedAt,
			}); err != nil {
				d.logf("Garden seed event %d committed but test-mode handling failed: %v", seq, err)
			}
		}
	})
}

func (d *Daemon) appendGardenSeedEventOnce(
	sourceKind, sourceID, replacedSourceID string, occurrence events.Occurrence,
) error {
	encoded, err := encodeGardenSeedEvents(occurrence)
	if err != nil {
		return err
	}
	seq, inserted, err := d.store.AppendBusEventOnceReplacingSource(sourceKind, sourceID, replacedSourceID, encoded[0], time.Now())
	if err != nil {
		return err
	}
	if inserted {
		announceGardenSeedEvents(d, []int64{seq})
	}
	return nil
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func gardenSeedLifecycleOccurrence(verb garden.Verb, seedID, causedBySessionID string, directlyNotifiedSessionID ...string) (events.Occurrence, error) {
	return gardenSeedLifecycleOccurrenceWithAttention(true, verb, seedID, causedBySessionID, directlyNotifiedSessionID...)
}

func quietGardenSeedLifecycleOccurrence(verb garden.Verb, seedID, causedBySessionID string, directlyNotifiedSessionID ...string) (events.Occurrence, error) {
	return gardenSeedLifecycleOccurrenceWithAttention(false, verb, seedID, causedBySessionID, directlyNotifiedSessionID...)
}

func gardenSeedLifecycleOccurrenceWithAttention(attentionRequested bool, verb garden.Verb, seedID, causedBySessionID string, directlyNotifiedSessionID ...string) (events.Occurrence, error) {
	payload := events.LifecyclePayload{
		AttentionRequested:        attentionRequested,
		CausedBySessionID:         strings.TrimSpace(causedBySessionID),
		DirectlyNotifiedSessionID: firstString(directlyNotifiedSessionID),
	}
	switch verb {
	case garden.VerbTend:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Tended, seedID, payload)
	case garden.VerbPark:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Parked, seedID, payload)
	case garden.VerbHarvest:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Harvested, seedID, payload)
	case garden.VerbWither:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Withered, seedID, payload)
	case garden.VerbReplant:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Replanted, seedID, payload)
	default:
		return events.Occurrence{}, fmt.Errorf("no Garden seed lifecycle event is declared for %q", verb)
	}
}

func recoveredGardenSeedEvents(seed garden.Seed, notes []garden.Note) ([]store.BusEvent, error) {
	planted, err := events.Occur(
		gardenSeedEventModel, gardenSeedEventVocabulary.Planted, seed.ID,
		events.CausePayload{CausedBySessionID: seed.PlanterSession},
	)
	if err != nil {
		return nil, err
	}
	occurrences := []events.Occurrence{planted}
	for _, edge := range seed.Edges {
		linked, err := events.Occur(
			gardenSeedEventModel, gardenSeedEventVocabulary.EdgeLinked, seed.ID,
			events.EdgePayload{EdgeKind: string(edge.Kind), TargetSeedID: edge.To},
		)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, linked)
	}
	if strings.TrimSpace(seed.ResumeSessionID) != "" {
		configured, err := events.Occur(
			gardenSeedEventModel, gardenSeedEventVocabulary.ResumeIdentityConfigured, seed.ID,
			events.CausePayload{CausedBySessionID: seed.PlanterSession},
		)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, configured)
	}
	var lifecycle garden.Verb
	switch seed.Status {
	case garden.StatusGrowing:
		lifecycle = garden.VerbTend
	case garden.StatusHarvested:
		lifecycle = garden.VerbHarvest
	case garden.StatusWithered:
		lifecycle = garden.VerbWither
	}
	if lifecycle != "" {
		moved, err := gardenSeedLifecycleOccurrence(lifecycle, seed.ID, "")
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, moved)
	}
	for _, note := range notes {
		noted, err := events.Occur(
			gardenSeedEventModel, gardenSeedEventVocabulary.NoteAdded, seed.ID,
			events.NoteAddedPayload{NoteID: note.ID, AttentionRequested: false},
		)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, noted)
	}
	return encodeGardenSeedEvents(occurrences...)
}

func (d *Daemon) registerGardenSeedEventConsumer() error {
	return d.eventBus.RegisterWithPreDrain(
		gardenSeedBellConsumer,
		bus.Filter{"garden.seed.*"},
		func(_ context.Context, _ bus.Consumer, gap *bus.Gap) error {
			if gap == nil {
				return nil
			}
			return fmt.Errorf(
				"garden seed event history is incomplete: consumer cursor=%d, earliest=%d, head=%d, missed=%d",
				gap.Cursor, gap.Earliest, gap.Head, gap.Missed,
			)
		},
		d.handleGardenSeedEvent,
	)
}

func (d *Daemon) validatePendingGardenSeedBells() error {
	if d.store == nil {
		return nil
	}
	names, err := d.store.PendingGardenSeedBellNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := gardenSeedEventModel.ValidatePendingBell(name); err != nil {
			return fmt.Errorf("garden seed mailbox cannot start: %w", err)
		}
	}
	return nil
}

func (d *Daemon) handleGardenSeedEvent(_ context.Context, event bus.Event) error {
	d.gardenWatchMu.Lock()
	defer d.gardenWatchMu.Unlock()
	return d.handleGardenSeedEventWithoutRoleLock(context.Background(), event)
}

func (d *Daemon) handleGardenSeedEventWithoutRoleLock(_ context.Context, event bus.Event) error {
	decision, err := gardenSeedEventModel.Interpret(event.Name, event.Subject, event.Payload)
	if err != nil {
		return err
	}

	var recipients []string
	if !decision.Quiet() {
		resolver, err := d.readGardenEventRoles()
		if err != nil {
			return err
		}
		recipients, err = gardenSeedEventModel.Recipients(event.Subject, decision, resolver)
		if err != nil {
			return err
		}
	}
	deliveries := make([]store.GardenSeedBellDelivery, len(recipients))
	for i, recipient := range recipients {
		deliveries[i] = store.GardenSeedBellDelivery{RecipientSessionID: recipient, ItemID: uuid.NewString()}
	}
	created, _, err := d.store.HandleGardenSeedEvent(
		event.Seq, event.Subject, strings.TrimPrefix(event.Name, "garden.seed."), decision.BellName(), deliveries, time.Now(),
	)
	if err != nil {
		return err
	}
	for _, sessionID := range created {
		d.noteQueuedAgentMailboxItem(sessionID)
		go d.drainQueuedAgentMailboxItems(sessionID)
	}
	return nil
}

type gardenEventRoles struct {
	daemon        *Daemon
	subscriptions gardenSubscriptions
}

func (d *Daemon) readGardenEventRoles() (gardenEventRoles, error) {
	subscriptions, err := d.readGardenSubscriptions()
	if err != nil {
		return gardenEventRoles{}, err
	}
	return gardenEventRoles{daemon: d, subscriptions: subscriptions}, nil
}

func (r gardenEventRoles) ResolveSeedRole(seedID string, role events.Role) ([]string, error) {
	if _, exists := r.subscriptions.seeds[seedID]; !exists {
		if _, _, err := r.daemon.readSeed(seedID); err != nil {
			return nil, fmt.Errorf("read seed %s for Garden bell eligibility: %w", seedID, err)
		}
		return nil, fmt.Errorf("seed %s is absent from the Garden role snapshot", seedID)
	}
	switch role {
	case events.CurrentTender:
		seed, exists := r.subscriptions.seeds[seedID]
		if !exists {
			return nil, nil
		}
		sessionID, err := r.daemon.localGardenTenderSession(seed.Tender())
		if err != nil || sessionID == "" {
			return nil, err
		}
		return []string{sessionID}, nil
	case events.CoveringWatchers:
		coverage, err := r.subscriptions.coverageChecked(seedID)
		if err != nil {
			return nil, err
		}
		sessions := make([]string, 0, len(coverage))
		for sessionID := range coverage {
			if r.daemon.store.Get(sessionID) != nil || r.daemon.store.DelegationSessionReserved(sessionID) {
				sessions = append(sessions, sessionID)
			}
		}
		return sessions, nil
	default:
		return nil, fmt.Errorf("unsupported Garden seed audience role %d", role)
	}
}

func (d *Daemon) discardIneligibleGardenSeedBellsLocked(sessionID string) error {
	items, err := d.store.UnreadGardenSeedMailboxItems(sessionID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return d.refreshAgentMailboxUnread(sessionID)
	}
	resolver, err := d.readGardenEventRoles()
	if err != nil {
		return err
	}
	var discarded []string
	for _, item := range items {
		eligible, err := gardenSeedEventModel.RecipientEligible(item.BellName, item.SeedID, sessionID, resolver)
		if err != nil {
			return err
		}
		if !eligible {
			discarded = append(discarded, item.SeedID)
		}
	}
	if err := d.store.DiscardGardenSeedMailboxItems(sessionID, discarded, time.Now()); err != nil {
		return err
	}
	return d.refreshAgentMailboxUnread(sessionID)
}

func (d *Daemon) discardAllIneligibleGardenSeedBellsLocked() error {
	items, err := d.store.PendingGardenSeedMailboxItems()
	if err != nil {
		return err
	}
	recipients := map[string]bool{}
	for _, item := range items {
		if sessionID := strings.TrimSpace(item.RecipientSessionID); sessionID != "" {
			recipients[sessionID] = true
		}
	}
	for sessionID := range recipients {
		if err := d.discardIneligibleGardenSeedBellsLocked(sessionID); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) invalidateGardenSeedParties(reason string) {
	if d == nil || d.store == nil {
		return
	}
	d.gardenWatchMu.Lock()
	err := d.discardAllIneligibleGardenSeedBellsLocked()
	d.gardenWatchMu.Unlock()
	if err != nil {
		d.logf("Garden seed mailbox invalidation after %s: %v", reason, err)
	}
}
