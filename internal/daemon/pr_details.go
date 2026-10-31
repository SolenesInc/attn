package daemon

import (
	"sync"

	"github.com/victorarias/attn/internal/github"
)

type prDetailFetches struct {
	mu       sync.Mutex
	started  uint64
	overlaps map[string]*prDetailOverlap
}

type prDetailOverlap struct {
	active   int
	accepted uint64
}

func (f *prDetailFetches) begin(id string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.overlaps == nil {
		f.overlaps = make(map[string]*prDetailOverlap)
	}
	overlap := f.overlaps[id]
	if overlap == nil {
		overlap = &prDetailOverlap{}
		f.overlaps[id] = overlap
	}
	overlap.active++
	f.started++
	return f.started
}

func (f *prDetailFetches) acceptUnlessSuperseded(id string, started uint64, apply func() (bool, error)) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	overlap := f.overlaps[id]
	if started < overlap.accepted {
		return false, nil
	}
	changed, err := apply()
	if err != nil {
		return false, err
	}
	overlap.accepted = started
	return changed, nil
}

func (f *prDetailFetches) finish(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	overlap := f.overlaps[id]
	if overlap.active--; overlap.active == 0 {
		delete(f.overlaps, id)
	}
}

func (d *Daemon) fetchPRDetails(client *github.Client, id, repo string, number int) (bool, error) {
	started := d.prDetailFetches.begin(id)
	defer d.prDetailFetches.finish(id)

	details, err := client.FetchPRDetails(repo, number)
	if err != nil {
		return false, err
	}
	return d.prDetailFetches.acceptUnlessSuperseded(id, started, func() (bool, error) {
		return d.store.UpdatePRDetails(id, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
	})
}
