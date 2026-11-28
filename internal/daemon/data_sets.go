package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/viewlang/catalog"
)

// dataSetKey names one data set as one profile sees it.
type dataSetKey struct {
	profileID string
	name      string
}

type heldDataSet struct {
	values   json.RawMessage
	decoded  any
	revision int
	next     time.Time
}

// dataSets holds the data sets that shown views name, shaped for the profile
// each is shown in, and reshapes them when a fact arrives or a set's next
// change comes. A set that reads as it did keeps its revision, so its tiles
// are sent nothing.
type dataSets struct {
	mu               sync.Mutex
	wake             chan struct{}
	wanted           map[dataSetKey]bool
	held             map[dataSetKey]heldDataSet
	revisions        int
	rateLimitedUntil string
}

func newDataSets() dataSets {
	return dataSets{wake: make(chan struct{}, 1), wanted: map[dataSetKey]bool{}, held: map[dataSetKey]heldDataSet{}}
}

func (sets *dataSets) nudge() {
	select {
	case sets.wake <- struct{}{}:
	default:
	}
}

// want takes the sets the shown views need now, forgets the others, and
// reshapes when one is new.
func (sets *dataSets) want(wanted map[dataSetKey]bool) {
	sets.mu.Lock()
	defer sets.mu.Unlock()
	fresh := false
	for key := range wanted {
		fresh = fresh || !sets.wanted[key]
	}
	for key := range sets.held {
		if !wanted[key] {
			delete(sets.held, key)
		}
	}
	sets.wanted = wanted
	if fresh {
		sets.nudge()
	}
}

func (d *Daemon) subscribeDataSetFacts() {
	d.dataSetsUnsubscribe = d.eventBus.Subscribe(bus.All, d.dataSetFactArrived)
}

func (d *Daemon) unsubscribeDataSetFacts() {
	if d.dataSetsUnsubscribe != nil {
		d.dataSetsUnsubscribe()
		d.dataSetsUnsubscribe = nil
	}
}

func (d *Daemon) dataSetFactArrived(ev bus.Event) {
	sets := &d.dataSets
	if ev.Name == FactRateLimited {
		if window, ok := decodeFact[rateLimitWindow](d, ev); ok {
			sets.mu.Lock()
			sets.rateLimitedUntil = window.ResetAt
			sets.mu.Unlock()
		}
	}
	sets.mu.Lock()
	shown := len(sets.wanted) > 0
	sets.mu.Unlock()
	if shown {
		sets.nudge()
	}
}

func (d *Daemon) runDataSets(done <-chan struct{}) {
	sets := &d.dataSets
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-done:
			timer.Stop()
			return
		case <-sets.wake:
		case <-timer.C:
		}
		next := d.reshapeDataSets()
		timer.Stop()
		if !next.IsZero() {
			timer.Reset(time.Until(next))
		}
	}
}

// reshapeDataSets shapes every wanted set again, tells the tiles when one
// changed, and says when the soonest of them next changes on its own.
func (d *Daemon) reshapeDataSets() time.Time {
	sets := &d.dataSets
	sets.mu.Lock()
	byProfile := map[string][]string{}
	for key := range sets.wanted {
		byProfile[key.profileID] = append(byProfile[key.profileID], key.name)
	}
	rateLimitedUntil := sets.rateLimitedUntil
	sets.mu.Unlock()

	now := time.Now()
	shaped := map[dataSetKey]heldDataSet{}
	for profileID, names := range byProfile {
		facts := d.dataSetFacts(profileID, rateLimitedUntil)
		for _, name := range names {
			values, next := shapeDataSet(name, facts, now)
			raw, err := json.Marshal(values)
			var decoded any
			if err == nil {
				err = json.Unmarshal(raw, &decoded)
			}
			if err != nil {
				d.logf("data set %s of %s: %v", name, profileID, err)
				continue
			}
			shaped[dataSetKey{profileID: profileID, name: name}] = heldDataSet{values: raw, decoded: decoded, next: next}
		}
	}

	sets.mu.Lock()
	changed := false
	var soonest time.Time
	for key, set := range shaped {
		if !sets.wanted[key] {
			continue
		}
		if was, held := sets.held[key]; held && bytes.Equal(was.values, set.values) {
			set.revision = was.revision
		} else {
			sets.revisions++
			set.revision = sets.revisions
			changed = true
		}
		sets.held[key] = set
		if !set.next.IsZero() && (soonest.IsZero() || set.next.Before(soonest)) {
			soonest = set.next
		}
	}
	sets.mu.Unlock()
	if changed {
		d.nudgeDesktopTileContent()
	}
	return soonest
}

// dataSetFacts reads what the app holds for profileID from the same reads the
// daemon sends the app.
func (d *Daemon) dataSetFacts(profileID, rateLimitedUntil string) *dataSetFacts {
	state := d.currentStateProjection()
	facts := &dataSetFacts{
		profileID:        profileID,
		sessions:         state.Sessions,
		settings:         map[string]string{},
		harnesses:        d.harnessCatalog(),
		outposts:         d.outpostsUpdatedMessage().Outposts,
		crew:             state.Crew,
		asks:             d.asksSnapshot(profileID, d.asks()).Asks,
		prs:              state.Prs,
		repos:            state.Repos,
		authors:          state.Authors,
		pollingOff:       gitHubPollingOffReason(),
		rateLimitedUntil: rateLimitedUntil,
	}
	facts.seeds, _, _ = d.seedsForBroadcast(profileID)
	for key, value := range d.settingsSnapshot(profileID) {
		if text, ok := value.(string); ok {
			facts.settings[key] = text
		}
	}
	if _, desktops, _, err := d.store.ProfileArrangementSeq(profileID); err != nil {
		d.logf("data sets: reading the desktops of %s: %v", profileID, err)
	} else if facts.desktops, err = d.protocolDesktops(desktops); err != nil {
		d.logf("data sets: the desktops of %s: %v", profileID, err)
	}
	return facts
}

// viewDataSets is the attn= sources of a view that name one of attn's data
// sets.
func viewDataSets(view *protocol.ViewDocument) []string {
	var names []string
	if view == nil {
		return nil
	}
	for _, source := range view.Sources {
		if source.Kind == protocol.ViewSourceKindAttn && slices.ContainsFunc(catalog.DataSets, func(set catalog.Named) bool { return set.Name == source.ID }) {
			names = append(names, source.ID)
		}
	}
	return names
}

// withDataSets is view with the values of its data sets as profileID sees
// them, and the newest revision among them; false while one is not shaped
// yet.
func (d *Daemon) withDataSets(view *protocol.ViewDocument, profileID string) (*protocol.ViewDocument, int, bool) {
	names := viewDataSets(view)
	if len(names) == 0 {
		return view, 0, true
	}
	sets := &d.dataSets
	sets.mu.Lock()
	held := make(map[string]heldDataSet, len(names))
	for _, name := range names {
		set, ok := sets.held[dataSetKey{profileID: profileID, name: name}]
		if !ok {
			sets.mu.Unlock()
			return nil, 0, false
		}
		held[name] = set
	}
	sets.mu.Unlock()

	filled := *view
	filled.Sources = slices.Clone(view.Sources)
	filled.Values = slices.Clone(view.Values)
	used, _ := json.Marshal(view.Values)
	budget := maxViewDataBytes - len(used)
	revision := 0
	for i := range filled.Sources {
		source := &filled.Sources[i]
		set, ok := held[source.ID]
		if source.Kind != protocol.ViewSourceKindAttn || !ok {
			continue
		}
		revision = max(revision, set.revision)
		if len(set.values) > budget {
			source.State, source.Notice = protocol.ViewSourceStateFailed, protocol.Ptr(fmt.Sprintf("attn's %s holds more than the %d bytes a view's sources hold together", source.ID, maxViewDataBytes))
			continue
		}
		budget -= len(set.values)
		source.State, source.Notice = protocol.ViewSourceStateOk, nil
		filled.Values = append(filled.Values, protocol.ViewValues{Source: source.ID, Values: set.decoded})
	}
	return &filled, revision, true
}
