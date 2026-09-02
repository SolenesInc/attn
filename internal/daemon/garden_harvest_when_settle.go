package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

func (d *Daemon) settleHarvestConditions() (harvested, cleared int) {
	if d.store == nil {
		return 0, 0
	}
	seeds, err := d.armedSeeds()
	if err != nil {
		d.logf("harvest-on-merge: reading armed seeds: %v", err)
		return 0, 0
	}
	for _, armed := range seeds {
		seed := armed.seed
		row, found := d.store.SessionPullRequestByID(seed.HarvestWhen.PullRequest)
		if !found {
			d.reportUntrackedHarvestCondition(seed)
			continue
		}
		switch row.State {
		case sessionPullRequestMerged:
			if _, _, err := d.fulfilHarvestWhen(seed, row, armed.rev); err != nil {
				d.logf("harvest-on-merge: harvesting %s on %s: %v", seed.ID, row.PRID, err)
				continue
			}
			harvested++
		case sessionPullRequestClosed:
			note := harvestWhenClosedNote(row)
			if _, _, err := d.clearHarvestWhen(seed.ID, seed.HarvestWhen.PullRequest, armed.rev, note, garden.Tender{Member: crew.DaemonID}); err != nil {
				d.logf("harvest-on-merge: clearing %s on %s: %v", seed.ID, row.PRID, err)
				continue
			}
			d.ringSeedActivity(seed.ID, harvestWhenRingCleared)
			cleared++
		}
	}
	return harvested, cleared
}

// The revision pins settlement to the condition observed here: a clear or a
// re-arm that lands in between makes the write a conflict, not a wrong harvest.
type armedSeed struct {
	seed garden.Seed
	rev  int64
}

func (d *Daemon) armedSeeds() ([]armedSeed, error) {
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Filters:    []docstore.Filter{{Field: "harvest_when_pull_request", Op: docstore.OpGt, Value: ""}},
		Limit:      gardenSnapshotLimit,
	})
	if err != nil {
		return nil, err
	}
	seeds := make([]armedSeed, 0, len(read.Documents))
	for _, doc := range read.Documents {
		seed, err := garden.Decode(doc.Body)
		if err != nil {
			d.logf("harvest-on-merge: seed %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		if seed.HarvestWhen == nil || strings.TrimSpace(seed.HarvestWhen.PullRequest) == "" {
			continue
		}
		seeds = append(seeds, armedSeed{seed: seed, rev: doc.Rev})
	}
	return seeds, nil
}

func (d *Daemon) reportUntrackedHarvestCondition(seed garden.Seed) {
	d.harvestWhenMu.Lock()
	if d.harvestWhenUntracked == nil {
		d.harvestWhenUntracked = map[string]bool{}
	}
	reported := d.harvestWhenUntracked[seed.ID]
	d.harvestWhenUntracked[seed.ID] = true
	d.harvestWhenMu.Unlock()
	if reported {
		return
	}
	d.logf("harvest-on-merge: %s waits on %s and no session records that pull request; it stays armed",
		seed.ID, seed.HarvestWhen.PullRequest)
}
