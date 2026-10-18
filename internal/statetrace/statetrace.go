package statetrace

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const DefaultCapacity = 256

type Outcome string

const (
	OutcomeApplied   Outcome = "applied"
	OutcomeDiscarded Outcome = "discarded"
	OutcomeVetoed    Outcome = "vetoed"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeObserved  Outcome = "observed"
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

func (o Observation) LogLine(sessionID protocol.SessionID) string {
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

type Ring struct {
	items []Observation
	start int
	size  int
}

func NewRing(capacity int) *Ring {
	return &Ring{items: make([]Observation, capacity)}
}

func (r *Ring) push(obs Observation) {
	capacity := len(r.items)
	if r.size < capacity {
		r.items[(r.start+r.size)%capacity] = obs
		r.size++
		return
	}
	r.items[r.start] = obs
	r.start = (r.start + 1) % capacity
}

func (r *Ring) newest() *Observation {
	if r.size == 0 {
		return nil
	}
	return &r.items[(r.start+r.size-1)%len(r.items)]
}

func (r *Ring) Snapshot() []Observation {
	if r == nil {
		return nil
	}
	out := make([]Observation, 0, r.size)
	capacity := len(r.items)
	for i := range r.size {
		out = append(out, r.items[(r.start+i)%capacity])
	}
	return out
}

func (r *Ring) Record(obs Observation) {
	if last := r.newest(); last != nil && last.sameEvidenceAs(obs) {
		last.Repeats++
		last.RecordedAt = obs.RecordedAt
		last.ObservedAt = obs.ObservedAt
		return
	}
	r.push(obs)
}
