package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type gardenCursorAttempt struct {
	cursor int64
	err    error
}

type failFirstGardenCursorStore struct {
	bus.Store
	attempts chan gardenCursorAttempt
	failed   bool
}

func (s *failFirstGardenCursorStore) SetCursor(name string, cursor int64, now time.Time) error {
	if name == gardenSeedBellConsumer && !s.failed {
		s.failed = true
		err := errors.New("cursor unavailable")
		s.attempts <- gardenCursorAttempt{cursor: cursor, err: err}
		return err
	}
	err := s.Store.SetCursor(name, cursor, now)
	if name == gardenSeedBellConsumer {
		s.attempts <- gardenCursorAttempt{cursor: cursor, err: err}
	}
	return err
}

func nextGardenCursorAttempt(t *testing.T, attempts <-chan gardenCursorAttempt) gardenCursorAttempt {
	t.Helper()
	select {
	case attempt := <-attempts:
		return attempt
	case <-time.After(5 * time.Second):
		t.Fatal("Garden seed consumer produced no cursor attempt")
		return gardenCursorAttempt{}
	}
}

func TestGardenSeedConsumerReplayAfterCursorFailureDoesNotRecreateAReadBell(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "watcher")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "durable bell replay"})
	watchSeed(t, d, "watcher", seed.ID, false)

	if err := d.startEventBus(); err != nil {
		t.Fatal(err)
	}
	d.stopEventBus()
	consumer, found, err := d.store.GetBusConsumer(gardenSeedBellConsumer)
	if err != nil || !found {
		t.Fatalf("initial consumer = %+v, found=%t err=%v", consumer, found, err)
	}

	occurrence, err := seedEvents.Occur(
		gardenSeedEventModel, gardenSeedEventVocabulary.NoteAdded, seed.ID,
		seedEvents.NoteAddedPayload{NoteID: "n-7k3f9m", AttentionRequested: true, CausedBySessionID: "sess-a"},
	)
	if err != nil {
		t.Fatal(err)
	}
	events, err := encodeGardenSeedEvents(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := d.store.AppendBusEvent(events[0], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if seq <= consumer.Cursor {
		t.Fatalf("event seq=%d did not land after consumer cursor=%d", seq, consumer.Cursor)
	}

	attempts := make(chan gardenCursorAttempt, 2)
	backing := &failFirstGardenCursorStore{Store: d.newSQLBusStore(), attempts: attempts}
	d.eventBus = bus.New(bus.Options{
		Store: backing, Log: d.logf, RetryBase: time.Nanosecond, RetryCap: time.Nanosecond,
	})
	d.gardenSeedEventConsumerErr = d.registerGardenSeedEventConsumer()
	if err := d.startEventBus(); err != nil {
		t.Fatal(err)
	}
	d.eventBus.Announce()

	first := nextGardenCursorAttempt(t, attempts)
	if first.cursor != seq || first.err == nil {
		t.Fatalf("first cursor attempt = %+v, want failed seq %d", first, seq)
	}
	assertOneSeedBell(t, d, "watcher", seed.ID, "note.added")
	if consumed, remaining, err := d.store.ReadGardenSeedMailboxItems("watcher", seed.ID, time.Now()); err != nil || !consumed || remaining != 0 {
		t.Fatalf("read queued bell: consumed=%t remaining=%d err=%v", consumed, remaining, err)
	}

	second := nextGardenCursorAttempt(t, attempts)
	if second.cursor != seq || second.err != nil {
		t.Fatalf("retry cursor attempt = %+v, want successful seq %d", second, seq)
	}
	if queued := queuedSeedBells(t, d, "watcher"); len(queued) != 0 {
		t.Fatalf("receipt replay recreated the read bell: %q", queued)
	}
}

func TestGardenSeedEventFirstHandlingUsesTheCurrentTender(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	addGardenSession(t, d, "sess-c")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "dispatch current tender"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")

	if _, _, err := d.applySeedTransition(seed.ID, garden.VerbTend, garden.Ask{
		Actor: garden.Tender{Session: "sess-b"}, Force: true,
	}); err != nil {
		t.Fatal(err)
	}
	occurrence, err := seedEvents.Occur(
		gardenSeedEventModel, gardenSeedEventVocabulary.Tended, seed.ID,
		seedEvents.LifecyclePayload{
			AttentionRequested: true, CausedBySessionID: "sess-a", DirectlyNotifiedSessionID: "sess-b",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := gardenSeedEventModel.Encode(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := d.store.AppendBusEvent(store.BusEvent{
		Name: encoded.Name, Subject: encoded.Subject, Payload: string(encoded.Payload), Source: "test",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := d.applySeedTransition(seed.ID, garden.VerbTend, garden.Ask{
		Actor: garden.Tender{Session: "sess-c"}, Force: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.handleGardenSeedEvent(context.Background(), bus.Event{
		Seq: seq, Name: encoded.Name, Subject: encoded.Subject, Payload: encoded.Payload, Source: "test",
	}); err != nil {
		t.Fatal(err)
	}

	for _, previousTender := range []string{"sess-a", "sess-b"} {
		if queued := queuedSeedBells(t, d, previousTender); len(queued) != 0 {
			t.Fatalf("previous tender %s received the delayed event: %q", previousTender, queued)
		}
	}
	assertOneSeedBell(t, d, "sess-c", seed.ID, "tended")
}

func TestQuietGardenSeedEventReceiptDoesNotDependOnLiveRoleState(t *testing.T) {
	d := newGardenDaemon(t)
	if err := d.handleSeedEventForTest(
		seedEvents.NameBodyEdited, "s-7k3f9m", seedEvents.CausePayload{CausedBySessionID: "sess-a"},
	); err != nil {
		t.Fatalf("quiet event for an absent seed: %v", err)
	}
	events, err := d.store.BusEventsSince(0, 100)
	if err != nil || len(events) == 0 {
		t.Fatalf("read quiet event: len=%d err=%v", len(events), err)
	}
	created, handled, err := d.store.HandleGardenSeedEvent(
		events[len(events)-1].Seq, "s-7k3f9m", "body.edited", "", nil, time.Now(),
	)
	if err != nil || handled || len(created) != 0 {
		t.Fatalf("quiet receipt replay = created=%v handled=%t err=%v", created, handled, err)
	}
}

func TestGardenSeedMailboxReconciliationDiscardsAMissingSeedWithoutStrandingOtherMail(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "legacy-recipient")
	addGardenSession(t, d, "peer-recipient")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "removed legacy seed"})
	watchSeed(t, d, "legacy-recipient", seed.ID, false)
	ringingNote(t, d, "sess-a", seed.ID, "queued before removal", true)
	assertOneSeedBell(t, d, "legacy-recipient", seed.ID, "note.added")

	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := d.store.DeleteDocument(*schema, seed.ID, nil); err != nil || !removed {
		t.Fatalf("delete legacy seed: removed=%v err=%v", removed, err)
	}
	if _, err := d.store.EnqueuePeerMessage(agentmailbox.PeerMessage{
		ID: "unrelated-mail", SenderSessionID: "sess-a", Body: "still owed",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, "peer-recipient"); err != nil {
		t.Fatal(err)
	}

	d.gardenWatchMu.Lock()
	err = d.discardAllIneligibleGardenSeedBellsLocked()
	d.gardenWatchMu.Unlock()
	if err != nil {
		t.Fatalf("reconcile missing seed bell: %v", err)
	}
	if queued := queuedSeedBells(t, d, "legacy-recipient"); len(queued) != 0 {
		t.Fatalf("missing seed bell survived reconciliation: %q", queued)
	}
	if d.hasQueuedAgentMailboxItems("peer-recipient") {
		t.Fatal("unrelated mail was already present in the fresh daemon's in-memory queue")
	}
	d.seedQueuedAgentMailboxItems()
	if !d.hasQueuedAgentMailboxItems("peer-recipient") {
		t.Fatal("missing seed bell stranded unrelated durable mail during startup reseeding")
	}
}

var _ bus.Store = (*failFirstGardenCursorStore)(nil)
