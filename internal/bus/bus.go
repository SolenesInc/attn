// See docs/plans/2026-08-01-ext-a1-event-bus.md.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRetention    = 30 * 24 * time.Hour
	DefaultTrimInterval = time.Hour
	DefaultBatchSize    = 200
	DefaultPollInterval = 5 * time.Second
	DefaultRetryBase    = time.Second
	DefaultRetryCap     = 2 * time.Minute
)

// LogFunc must never be log.Printf: the daemon's background stderr is discarded.
type LogFunc func(format string, args ...interface{})

type Event struct {
	Seq       int64
	Name      string
	Subject   string
	Payload   json.RawMessage
	Source    string
	CreatedAt time.Time
}

func (e Event) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

type Consumer struct {
	Name          string
	Cursor        int64
	Filter        string
	Enabled       bool
	PinsRetention bool
	UpdatedAt     time.Time
}

type Store interface {
	Append(e Event, now time.Time) (int64, error)
	Since(cursor int64, limit int) ([]Event, error)
	Bounds() (earliest, head int64, err error)
	GetConsumer(name string) (Consumer, bool, error)
	SaveConsumer(c Consumer, now time.Time) error
	DeleteConsumer(name string) error
	SetCursor(name string, cursor int64, now time.Time) error
	ListConsumers() ([]Consumer, error)
	Trim(cutoff time.Time) (int, error)
	Compact(names []string, floor int64) (int, error)
	Producers(cutoffs []time.Time) ([]ProducerRow, error)
	EventTimeAt(seq int64) (time.Time, bool, error)
	PendingBytes(above int64) (int64, error)
}

// An error stalls the consumer and redelivers the event; handlers must tolerate redelivery.
type Handler func(ctx context.Context, ev Event) error

type Gap struct {
	Cursor   int64
	Earliest int64
	Head     int64
	Missed   int64
}

// PreDrain may durably move the cursor; the bus re-reads the registration after it returns.
type PreDrain func(ctx context.Context, consumer Consumer, gap *Gap) error

type Options struct {
	Store        Store
	Log          LogFunc
	Now          func() time.Time
	Retention    time.Duration
	TrimInterval time.Duration
	BatchSize    int
	PollInterval time.Duration
	RetryBase    time.Duration
	RetryCap     time.Duration
	PinAlarmAge  time.Duration
	Compactable  []string
}

type Bus struct {
	store Store
	log   LogFunc
	now   func() time.Time

	retention    time.Duration
	trimInterval time.Duration
	batchSize    int
	pollInterval time.Duration
	retryBase    time.Duration
	retryCap     time.Duration
	pinAlarmAge  time.Duration

	compactable []string

	publishMu sync.Mutex
	marked    bool
	announced int64

	mu        sync.Mutex
	durables  []*durable
	ephemeral map[int]*ephemeralSub
	nextSubID int
	started   bool
	// stopped is set before Stop cancels, so a registration racing shutdown does not add to wg.
	stopped  bool
	retiring map[string]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type durable struct {
	name     string
	handler  Handler
	preDrain PreDrain

	wake chan struct{}

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	launched bool

	mu sync.Mutex
	// filter shares the position's lock: SetFilter changes it while the delivery loop reads it.
	filter   Filter
	cursor   int64
	enabled  bool
	stalled  string
	failures int
	retired  bool
}

type ephemeralSub struct {
	filter Filter
	fn     func(Event)
}

func New(opts Options) *Bus {
	b := &Bus{
		store:        opts.Store,
		log:          opts.Log,
		now:          opts.Now,
		retention:    nonZeroDuration(opts.Retention, DefaultRetention),
		trimInterval: nonZeroDuration(opts.TrimInterval, DefaultTrimInterval),
		batchSize:    nonZeroInt(opts.BatchSize, DefaultBatchSize),
		pollInterval: nonZeroDuration(opts.PollInterval, DefaultPollInterval),
		retryBase:    nonZeroDuration(opts.RetryBase, DefaultRetryBase),
		retryCap:     nonZeroDuration(opts.RetryCap, DefaultRetryCap),
		pinAlarmAge:  pinAlarmAgeOrDefault(opts.PinAlarmAge),
		compactable:  append([]string(nil), opts.Compactable...),
		ephemeral:    map[int]*ephemeralSub{},
		retiring:     map[string]struct{}{},
	}
	if b.now == nil {
		b.now = time.Now
	}
	if b.log == nil {
		b.log = func(string, ...interface{}) {}
	}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	// Mark at construction, not Start: an unplaced mark replays the whole log on first write.
	if b.store != nil {
		b.markHead()
	}
	return b
}

func (b *Bus) Publish(name, subject string, payload any) (int64, error) {
	return b.publish(Event{Name: name, Subject: subject}, payload)
}

func (b *Bus) PublishFrom(source, name, subject string, payload any) (int64, error) {
	return b.publish(Event{Name: name, Subject: subject, Source: source}, payload)
}

func (b *Bus) publish(ev Event, payload any) (int64, error) {
	if strings.TrimSpace(ev.Name) == "" {
		return 0, errors.New("bus: publish requires an event name")
	}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("bus: marshaling payload for %s: %w", ev.Name, err)
		}
		ev.Payload = raw
	}
	b.publishMu.Lock()
	defer b.publishMu.Unlock()

	now := b.now()
	ev.CreatedAt = now

	if b.store == nil {
		b.fanoutEphemeral(ev)
		return 0, nil
	}

	seq, err := b.store.Append(ev, now)
	if err != nil {
		// A failed append must not silence the wire.
		b.fanoutEphemeral(ev)
		return 0, fmt.Errorf("bus: appending %s: %w", ev.Name, err)
	}
	ev.Seq = seq

	// Read the log forward rather than deliver the event in hand: a fact appended by another transaction may sit below this seq unannounced.
	b.announceLocked(&ev)
	b.wakeDurables()
	return seq, nil
}

func (b *Bus) Announce() {
	if b.store == nil {
		return
	}
	b.publishMu.Lock()
	defer b.publishMu.Unlock()
	b.announceLocked(nil)
	b.wakeDurables()
}

func (b *Bus) markHead() bool {
	_, head, err := b.store.Bounds()
	if err != nil {
		b.log("bus: reading log bounds to place the announce mark: %v", err)
		return false
	}
	b.announced = head
	b.marked = true
	return true
}

func (b *Bus) announceLocked(fallback *Event) {
	if !b.marked && !b.markHead() {
		if fallback != nil {
			b.fanoutEphemeral(*fallback)
		}
		return
	}
	for {
		events, err := b.store.Since(b.announced, b.batchSize)
		if err != nil {
			b.log("bus: reading the log forward from seq %d to announce: %v", b.announced, err)
			if fallback != nil && fallback.Seq > b.announced {
				b.fanoutEphemeral(*fallback)
				b.announced = fallback.Seq
			}
			return
		}
		if len(events) == 0 {
			return
		}
		for _, ev := range events {
			b.fanoutEphemeral(ev)
			b.announced = ev.Seq
		}
		if len(events) < b.batchSize {
			return
		}
	}
}

func (b *Bus) fanoutEphemeral(ev Event) {
	b.mu.Lock()
	subs := make([]*ephemeralSub, 0, len(b.ephemeral))
	for _, s := range b.ephemeral {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	for _, s := range subs {
		if s.filter.Matches(ev.Name) {
			s.fn(ev)
		}
	}
}

func (b *Bus) wakeDurables() {
	b.mu.Lock()
	ds := append([]*durable(nil), b.durables...)
	b.mu.Unlock()

	for _, d := range ds {
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
}

func (b *Bus) Register(name string, filter Filter, h Handler) error {
	return b.register(name, filter, nil, h)
}

func (b *Bus) RegisterWithPreDrain(name string, filter Filter, pre PreDrain, h Handler) error {
	if pre == nil {
		return fmt.Errorf("bus: consumer %s needs a pre-drain hook", name)
	}
	return b.register(name, filter, pre, h)
}

func (b *Bus) register(name string, filter Filter, pre PreDrain, h Handler) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("bus: consumer name is required")
	}
	if h == nil {
		return fmt.Errorf("bus: consumer %s needs a handler", name)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.durables {
		if d.name == name {
			return fmt.Errorf("bus: consumer %s already registered", name)
		}
	}
	// A name being unregistered stays claimed until its row is gone: resuming a cursor from a row about to be deleted leaves a loop retrying a vanished registration forever.
	if _, retiring := b.retiring[name]; retiring {
		return fmt.Errorf("bus: consumer %s is being unregistered; retry once it is gone", name)
	}
	d := b.newDurable(name, filter, pre, h)

	if !b.started || b.store == nil {
		b.durables = append(b.durables, d)
		return nil
	}

	_, head, err := b.store.Bounds()
	if err != nil {
		d.cancel()
		return fmt.Errorf("bus: reading log bounds to register %s: %w", name, err)
	}
	if err := b.initConsumer(d, head); err != nil {
		d.cancel()
		return err
	}
	b.durables = append(b.durables, d)
	b.launchLocked(d)
	return nil
}

// Cancel, wait for the loop to exit, then delete the row: deleting first leaves a live loop retrying a vanished registration forever.
func (b *Bus) Unregister(name string) error {
	b.mu.Lock()
	var (
		found *durable
		wait  bool
	)
	for i, d := range b.durables {
		if d.name != name {
			continue
		}
		found = d
		wait = d.launched
		b.durables = append(b.durables[:i], b.durables[i+1:]...)
		break
	}
	b.retiring[name] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.retiring, name)
		b.mu.Unlock()
	}()

	if found != nil {
		found.retire()
		found.cancel()
		if wait {
			<-found.done
		}
	}

	if b.store == nil {
		return nil
	}
	if err := b.store.DeleteConsumer(name); err != nil {
		return fmt.Errorf("bus: deleting consumer %s: %w", name, err)
	}
	return nil
}

// Unregister-then-Register would delete the cursor, silently skipping everything published meanwhile.
func (b *Bus) SetFilter(name string, filter Filter) error {
	b.mu.Lock()
	var found *durable
	for _, d := range b.durables {
		if d.name == name {
			found = d
			break
		}
	}
	started := b.started
	b.mu.Unlock()
	if found == nil {
		return fmt.Errorf("bus: consumer %s is not registered, so its filter cannot be changed", name)
	}
	// Persist first, then swap what the loop reads: the other order leaves the loop filtering by a rule no restart would reproduce.
	if b.store != nil && started {
		existing, ok, err := b.store.GetConsumer(name)
		if err != nil {
			return fmt.Errorf("bus: reading consumer %s to change its filter: %w", name, err)
		}
		if !ok {
			return fmt.Errorf("bus: consumer %s has no registration to change", name)
		}
		if err := b.store.SaveConsumer(Consumer{
			Name:    name,
			Cursor:  existing.Cursor,
			Filter:  filter.String(),
			Enabled: existing.Enabled,
		}, b.now()); err != nil {
			return fmt.Errorf("bus: saving the filter of consumer %s: %w", name, err)
		}
	}
	found.setFilter(filter)
	return nil
}

func (b *Bus) Registered(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.durables {
		if d.name == name {
			return true
		}
	}
	return false
}

func (b *Bus) newDurable(name string, filter Filter, pre PreDrain, h Handler) *durable {
	ctx, cancel := context.WithCancel(b.ctx)
	return &durable{
		name:     name,
		filter:   filter,
		handler:  h,
		preDrain: pre,
		wake:     make(chan struct{}, 1),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		enabled:  true,
	}
}

// Caller holds b.mu, which orders the WaitGroup increment against Stop.
func (b *Bus) launchLocked(d *durable) {
	if b.stopped || b.ctx.Err() != nil {
		close(d.done)
		return
	}
	d.launched = true
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer close(d.done)
		b.deliver(d)
	}()
}

// fn runs inline on the publishing goroutine holding publishMu: it must be cheap and must not publish back onto the bus (deadlock).
func (b *Bus) Subscribe(filter Filter, fn func(Event)) func() {
	if fn == nil {
		return func() {}
	}
	b.mu.Lock()
	id := b.nextSubID
	b.nextSubID++
	b.ephemeral[id] = &ephemeralSub{filter: filter, fn: fn}
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		delete(b.ephemeral, id)
		b.mu.Unlock()
	}
}

func (b *Bus) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return nil
	}
	b.started = true

	if b.store == nil {
		return nil
	}

	_, head, err := b.store.Bounds()
	if err != nil {
		return fmt.Errorf("bus: reading log bounds: %w", err)
	}

	for _, d := range b.durables {
		if err := b.initConsumer(d, head); err != nil {
			return err
		}
		b.launchLocked(d)
	}

	if b.stopped {
		return nil
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.retain()
	}()
	return nil
}

func (b *Bus) initConsumer(d *durable, head int64) error {
	existing, ok, err := b.store.GetConsumer(d.name)
	if err != nil {
		return fmt.Errorf("bus: loading consumer %s: %w", d.name, err)
	}
	now := b.now()
	if !ok {
		if err := b.store.SaveConsumer(Consumer{
			Name:    d.name,
			Cursor:  head,
			Filter:  d.filterExpr(),
			Enabled: true,
		}, now); err != nil {
			return fmt.Errorf("bus: registering consumer %s: %w", d.name, err)
		}
		d.setPosition(head, true)
		return nil
	}
	if err := b.store.SaveConsumer(Consumer{
		Name:    d.name,
		Cursor:  existing.Cursor,
		Filter:  d.filterExpr(),
		Enabled: existing.Enabled,
	}, now); err != nil {
		return fmt.Errorf("bus: updating consumer %s: %w", d.name, err)
	}
	d.setPosition(existing.Cursor, existing.Enabled)
	return nil
}

func (b *Bus) Stop() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()

	b.cancel()
	b.wg.Wait()
}

func (b *Bus) deliver(d *durable) {
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	retry := time.Duration(0)
	for {
		if retry > 0 {
			timer := time.NewTimer(retry)
			select {
			case <-d.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			retry = 0
		} else {
			select {
			case <-d.ctx.Done():
				return
			case <-d.wake:
			case <-ticker.C:
			}
		}

		failures := d.drainFailures()
		if err := b.drain(d); err != nil {
			if d.ctx.Err() != nil {
				return
			}
			retry = backoff(b.retryBase, b.retryCap, failures+1)
			d.recordFailure(err.Error(), failures+1)
			b.log("bus: consumer %s stalled at seq %d (attempt %d, retry in %s): %v",
				d.name, d.position()+1, failures+1, retry, err)
		}
	}
}

func (b *Bus) drain(d *durable) error {
	prepare := func() (bool, error) {
		// The enabled bit is the kill switch and lives only in the database; never cache it.
		rec, ok, err := b.store.GetConsumer(d.name)
		if err != nil {
			return false, fmt.Errorf("reading registration: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("registration for %s disappeared", d.name)
		}
		d.setPosition(rec.Cursor, rec.Enabled)
		if !rec.Enabled {
			return false, nil
		}

		earliest, head, err := b.store.Bounds()
		if err != nil {
			return false, fmt.Errorf("reading log bounds: %w", err)
		}
		var gap *Gap
		if earliest != 0 && d.position() < earliest-1 {
			gap = &Gap{
				Cursor: d.position(), Earliest: earliest, Head: head,
				Missed: earliest - 1 - d.position(),
			}
		}
		if d.preDrain == nil {
			if gap != nil {
				if err := b.reconcileGap(d, *gap); err != nil {
					return false, err
				}
			}
			return true, nil
		}
		if err := d.preDrain(d.ctx, rec, gap); err != nil {
			return false, fmt.Errorf("before draining: %w", err)
		}
		rec, ok, err = b.store.GetConsumer(d.name)
		if err != nil {
			return false, fmt.Errorf("reading registration after pre-drain: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("registration for %s disappeared during pre-drain", d.name)
		}
		d.setPosition(rec.Cursor, rec.Enabled)
		return rec.Enabled, nil
	}

	if d.preDrain == nil {
		ready, err := prepare()
		if err != nil || !ready {
			return err
		}
	}

	lastCheck := b.now()
	killed := func() (bool, error) {
		if b.now().Sub(lastCheck) < b.pollInterval {
			return false, nil
		}
		lastCheck = b.now()
		rec, ok, err := b.store.GetConsumer(d.name)
		if err != nil {
			return false, fmt.Errorf("reading registration: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("registration for %s disappeared", d.name)
		}
		d.setEnabled(rec.Enabled)
		return !rec.Enabled, nil
	}

	for {
		if d.ctx.Err() != nil {
			return nil
		}
		if d.preDrain != nil {
			ready, err := prepare()
			if err != nil || !ready {
				return err
			}
		}
		events, err := b.store.Since(d.position(), b.batchSize)
		if err != nil {
			return fmt.Errorf("reading log: %w", err)
		}
		if len(events) == 0 {
			d.clearFailure()
			return nil
		}

		var skipped int64
		for _, ev := range events {
			if d.ctx.Err() != nil {
				return nil
			}
			stop, err := killed()
			if err != nil {
				return err
			}
			if stop {
				if skipped != 0 {
					return b.advance(d, skipped)
				}
				return nil
			}
			if !d.matches(ev.Name) {
				skipped = ev.Seq
				continue
			}
			if skipped != 0 {
				if err := b.advance(d, skipped); err != nil {
					return err
				}
				skipped = 0
			}
			if err := d.handler(d.ctx, ev); err != nil {
				if d.ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("handling %s (seq %d): %w", ev.Name, ev.Seq, err)
			}
			if err := b.advance(d, ev.Seq); err != nil {
				return err
			}
			d.clearFailure()
		}
		if skipped != 0 {
			if err := b.advance(d, skipped); err != nil {
				return err
			}
		}
	}
}

func (b *Bus) reconcileGap(d *durable, gap Gap) error {
	b.log("bus: consumer %s resumed at head %d; %d event(s) were trimmed before its cursor %d",
		d.name, gap.Head, gap.Missed, gap.Cursor)
	return b.advance(d, gap.Head)
}

func (b *Bus) advance(d *durable, seq int64) error {
	// A handler in flight when Unregister landed has no cursor to move; erroring would stall a consumer nobody serves.
	if d.isRetired() {
		return nil
	}
	if err := b.store.SetCursor(d.name, seq, b.now()); err != nil {
		return fmt.Errorf("persisting cursor: %w", err)
	}
	d.setCursor(seq)
	return nil
}

func (b *Bus) retain() {
	ticker := time.NewTicker(b.trimInterval)
	defer ticker.Stop()
	b.ReportLoudProducers()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			_, _ = b.Trim()
			b.ReportLoudProducers()
		}
	}
}

func (b *Bus) ReportLoudProducers() {
	if b.store == nil {
		return
	}
	status, err := b.Status()
	if err != nil {
		b.log("bus: reading the log to check producer rates: %v", err)
		return
	}
	for _, h := range status.Health {
		if h.Kind == HealthProducerSurging {
			b.log("bus: %s", h.Message)
		}
	}
}

func (b *Bus) Trim() (int, error) {
	if b.store == nil {
		return 0, nil
	}
	var (
		removed int
		failed  error
	)
	n, err := b.store.Trim(b.now().Add(-b.retention))
	if err != nil {
		failed = fmt.Errorf("retention pass: %w", err)
		b.log("bus: retention pass failed: %v", err)
	} else {
		removed += n
		if n > 0 {
			b.log("bus: trimmed %d event(s) older than %s", n, b.retention)
		}
	}
	compacted, err := b.compact()
	if err != nil && failed == nil {
		failed = err
	}
	return removed + compacted, failed
}

// Compacting above the cursor floor punches holes that reconcileGap misreads as trimmed history.
// floor punches holes that reconcileGap misreads as trimmed history.
func (b *Bus) compact() (int, error) {
	if len(b.compactable) == 0 {
		return 0, nil
	}
	floor, err := b.consumerFloor()
	if err != nil {
		b.log("bus: compaction pass failed: %v", err)
		return 0, fmt.Errorf("compaction pass: %w", err)
	}
	n, err := b.store.Compact(b.compactable, floor)
	if err != nil {
		b.log("bus: compaction pass failed: %v", err)
		return 0, fmt.Errorf("compaction pass: %w", err)
	}
	if n > 0 {
		b.log("bus: compacted %d superseded event(s) at or below seq %d", n, floor)
	}
	return n, nil
}

func (b *Bus) consumerFloor() (int64, error) {
	rows, err := b.store.ListConsumers()
	if err != nil {
		return 0, fmt.Errorf("listing consumers: %w", err)
	}
	floor := int64(-1)
	for _, c := range rows {
		if !c.Enabled && !c.PinsRetention {
			continue
		}
		if floor < 0 || c.Cursor < floor {
			floor = c.Cursor
		}
	}
	if floor >= 0 {
		return floor, nil
	}
	_, head, err := b.store.Bounds()
	if err != nil {
		return 0, fmt.Errorf("reading log bounds: %w", err)
	}
	return head, nil
}

func (d *durable) matches(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.filter.Matches(name)
}

func (d *durable) filterExpr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.filter.String()
}

func (d *durable) setFilter(f Filter) {
	d.mu.Lock()
	d.filter = f
	d.mu.Unlock()
}

func (d *durable) position() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cursor
}

func (d *durable) setCursor(seq int64) {
	d.mu.Lock()
	d.cursor = seq
	d.mu.Unlock()
}

func (d *durable) setPosition(cursor int64, enabled bool) {
	d.mu.Lock()
	d.cursor = cursor
	d.enabled = enabled
	d.mu.Unlock()
}

func (d *durable) setEnabled(enabled bool) {
	d.mu.Lock()
	d.enabled = enabled
	d.mu.Unlock()
}

// retire makes a late result from an in-flight handler a no-op rather than a failure.
func (d *durable) retire() {
	d.mu.Lock()
	d.retired = true
	d.mu.Unlock()
}

func (d *durable) isRetired() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.retired
}

func (d *durable) recordFailure(reason string, count int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.retired {
		return
	}
	d.stalled = reason
	d.failures = count
}

func (d *durable) clearFailure() {
	d.mu.Lock()
	d.stalled = ""
	d.failures = 0
	d.mu.Unlock()
}

func (d *durable) drainFailures() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.failures
}

func (d *durable) stallReason() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stalled
}

func backoff(base, ceiling time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= ceiling {
			return ceiling
		}
	}
	if d > ceiling {
		return ceiling
	}
	return d
}

func pinAlarmAgeOrDefault(v time.Duration) time.Duration {
	if v == 0 {
		return DefaultPinAlarmAge
	}
	return v
}

const RetentionEnv = "ATTN_BUS_RETENTION"

// The daemon's hourly pass and `attn bus trim` read one database; a window they disagree about makes the CLI remove rows the daemon would have kept.
func RetentionFromEnv(log LogFunc) time.Duration {
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	raw := strings.TrimSpace(os.Getenv(RetentionEnv))
	if raw == "" {
		return DefaultRetention
	}
	window, err := time.ParseDuration(raw)
	if err != nil {
		log("bus: %s=%q is not a duration (%v); using the default %s",
			RetentionEnv, raw, err, DefaultRetention)
		return DefaultRetention
	}
	if window <= 0 {
		log("bus: %s=%q is not a positive window; using the default %s",
			RetentionEnv, raw, DefaultRetention)
		return DefaultRetention
	}
	log("bus: retention window set to %s by %s (default %s)", window, RetentionEnv, DefaultRetention)
	return window
}

const PinAlarmAgeEnv = "ATTN_BUS_PIN_ALARM_AGE"

// The daemon raising the alarm and the CLI reading the same database must draw the line in the same place.
func PinAlarmAgeFromEnv(log LogFunc) time.Duration {
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	raw := strings.TrimSpace(os.Getenv(PinAlarmAgeEnv))
	if raw == "" {
		return DefaultPinAlarmAge
	}
	age, err := time.ParseDuration(raw)
	if err != nil {
		log("bus: %s=%q is not a duration (%v); using the default %s",
			PinAlarmAgeEnv, raw, err, DefaultPinAlarmAge)
		return DefaultPinAlarmAge
	}
	if age <= 0 {
		log("bus: %s=%q — the retention-pin alarm is off; a stuck consumer will grow the log unannounced",
			PinAlarmAgeEnv, raw)
		// Negative is the off switch inside the bus; zero would read as "unset".
		return -1
	}
	log("bus: retention-pin alarm set to %s by %s (default %s)", age, PinAlarmAgeEnv, DefaultPinAlarmAge)
	return age
}

func nonZeroDuration(v, fallback time.Duration) time.Duration {
	if v <= 0 {
		return fallback
	}
	return v
}

func nonZeroInt(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
