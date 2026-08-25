package statetrace

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// At one observation per second, a bit over three minutes of history.
const DefaultCapacity = 256

type Outcome string

const (
	OutcomeApplied Outcome = "applied"
	// OutcomeDiscarded means applyState's commit rule refused it.
	OutcomeDiscarded Outcome = "discarded"
	// OutcomeVetoed means it was rejected before ever reaching applyState.
	OutcomeVetoed   Outcome = "vetoed"
	OutcomeSkipped  Outcome = "skipped"
	OutcomeObserved Outcome = "observed"
)

type Observation struct {
	Source     string
	Claim      string
	Detail     string
	Cause      string
	Outcome    Outcome
	Reason     string
	ObservedAt time.Time
	RecordedAt time.Time
	Repeats    int
}

func (o Observation) sameEvidenceAs(other Observation) bool {
	return o.Source == other.Source &&
		o.Claim == other.Claim &&
		o.Cause == other.Cause &&
		o.Outcome == other.Outcome &&
		o.Reason == other.Reason &&
		o.Detail == other.Detail
}

func (o Observation) LogLine(sessionID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "state trace: session=%s source=%s claim=%s outcome=%s", sessionID, orDash(o.Source), orDash(o.Claim), orDash(string(o.Outcome)))
	if o.Cause != "" {
		fmt.Fprintf(&b, " cause=%s", o.Cause)
	}
	if o.Reason != "" {
		fmt.Fprintf(&b, " reason=%s", o.Reason)
	}
	if o.Detail != "" {
		fmt.Fprintf(&b, " detail=%q", o.Detail)
	}
	if !o.ObservedAt.IsZero() {
		fmt.Fprintf(&b, " observed_at=%s", o.ObservedAt.Format(time.RFC3339Nano))
		if !o.RecordedAt.IsZero() {
			fmt.Fprintf(&b, " lag=%s", o.RecordedAt.Sub(o.ObservedAt).Round(time.Millisecond))
		}
	}
	return b.String()
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

type Recorder struct {
	mu       sync.Mutex
	capacity int
	rings    map[string]*ring
}

func New(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Recorder{capacity: capacity, rings: make(map[string]*ring)}
}

func (r *Recorder) Capacity() int {
	if r == nil {
		return 0
	}
	return r.capacity
}

func (r *Recorder) Record(sessionID string, obs Observation) {
	r.RecordIf(sessionID, obs, nil)
}

// admit runs under the recorder's lock, so the check is atomic with Forget (checking before
// Record can recreate a ring nothing forgets); it must not call in or take a held lock.
func (r *Recorder) RecordIf(sessionID string, obs Observation, admit func() bool) {
	if r == nil || sessionID == "" {
		return
	}
	if obs.RecordedAt.IsZero() {
		obs.RecordedAt = time.Now()
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = obs.RecordedAt
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if admit != nil && !admit() {
		return
	}
	target := r.rings[sessionID]
	if target == nil {
		target = newRing(r.capacity)
		r.rings[sessionID] = target
	}
	if last := target.newest(); last != nil && last.sameEvidenceAs(obs) {
		last.Repeats++
		last.RecordedAt = obs.RecordedAt
		last.ObservedAt = obs.ObservedAt
		return
	}
	target.push(obs)
}

func (r *Recorder) Observations(sessionID string) []Observation {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target := r.rings[sessionID]
	if target == nil {
		return nil
	}
	return target.snapshot()
}

func (r *Recorder) SessionCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rings)
}

func (r *Recorder) Forget(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rings, sessionID)
}

type ring struct {
	items []Observation
	start int
	size  int
}

func newRing(capacity int) *ring {
	return &ring{items: make([]Observation, capacity)}
}

func (r *ring) push(obs Observation) {
	capacity := len(r.items)
	if r.size < capacity {
		r.items[(r.start+r.size)%capacity] = obs
		r.size++
		return
	}
	r.items[r.start] = obs
	r.start = (r.start + 1) % capacity
}

// newest points into the ring's own storage (nil when empty) so a collapsing repeat
// can update it in place.
func (r *ring) newest() *Observation {
	if r.size == 0 {
		return nil
	}
	return &r.items[(r.start+r.size-1)%len(r.items)]
}

func (r *ring) snapshot() []Observation {
	out := make([]Observation, 0, r.size)
	capacity := len(r.items)
	for i := range r.size {
		out = append(out, r.items[(r.start+i)%capacity])
	}
	return out
}
