package daemon

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

const harvestWhenClearedRing = "harvest-on-merge cleared"

// Every armed seed whose pull request has finished: a merge harvests the seed,
// a close without merging clears the condition and rings whoever watches.
func (d *Daemon) settleHarvestConditions() (harvested, cleared int) {
	if d.store == nil {
		return 0, 0
	}
	seeds, err := d.armedSeeds()
	if err != nil {
		d.logf("harvest-on-merge: reading armed seeds: %v", err)
		return 0, 0
	}
	for _, seed := range seeds {
		row, found := d.store.SessionPullRequestByID(seed.HarvestWhen.PullRequest)
		if !found {
			d.reportUntrackedHarvestCondition(seed)
			continue
		}
		switch row.State {
		case sessionPullRequestMerged:
			if err := d.fulfilHarvestWhen(seed, row); err != nil {
				d.logf("harvest-on-merge: harvesting %s on %s: %v", seed.ID, row.PRID, err)
				continue
			}
			harvested++
		case sessionPullRequestClosed:
			note := fmt.Sprintf("PR #%d closed without merging; harvest-on-merge cleared", row.Number)
			if err := d.clearHarvestWhen(seed, note); err != nil {
				d.logf("harvest-on-merge: clearing %s on %s: %v", seed.ID, row.PRID, err)
				continue
			}
			d.ringSeedActivity(seed.ID, harvestWhenClearedRing)
			cleared++
		}
	}
	return harvested, cleared
}

func (d *Daemon) armedSeeds() ([]garden.Seed, error) {
	// The whole garden fits one query (gardenSnapshotLimit); the armed few always do.
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Filters:    []docstore.Filter{{Field: "harvest_when_pull_request", Op: docstore.OpGt, Value: ""}},
		Limit:      gardenSnapshotLimit,
	})
	if err != nil {
		return nil, err
	}
	seeds := make([]garden.Seed, 0, len(read.Documents))
	for _, doc := range read.Documents {
		seed, err := garden.Decode(doc.Body)
		if err != nil {
			d.logf("harvest-on-merge: seed %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		if seed.HarvestWhen == nil || strings.TrimSpace(seed.HarvestWhen.PullRequest) == "" {
			continue
		}
		seeds = append(seeds, seed)
	}
	return seeds, nil
}

// A condition nobody records status for would say the same thing on every tick,
// so it is said once per seed per daemon life.
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

func (d *Daemon) fulfilHarvestWhen(seed garden.Seed, row store.SessionPullRequestRecord) error {
	reason := harvestWhenReason(row)
	// Forced: the merge is the answer whoever holds the seed, and the log records
	// the takeover.
	if _, _, _, err := d.applySeedTransitionDetailedAs(seed.ID, garden.VerbHarvest, garden.Ask{
		Actor:  garden.Tender{Member: crew.DaemonID},
		Reason: reason,
		Force:  true,
	}, "", d.sessionExists); err != nil {
		return err
	}
	d.mirrorSeedMoveOntoTicket("", seed.ID, garden.VerbHarvest, reason)
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbHarvest])
	return nil
}

func (d *Daemon) clearHarvestWhen(seed garden.Seed, note string) error {
	schema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	current, doc, err := d.readSeed(seed.ID)
	if err != nil {
		return err
	}
	if current.HarvestWhen == nil {
		return nil
	}
	current.HarvestWhen = nil
	if _, err := d.writeSeed(*schema, current, doc.Rev, FactGardenHarvestWhenChanged); err != nil {
		return err
	}
	if strings.TrimSpace(note) == "" {
		return nil
	}
	if _, err := d.appendSeedNote(seed.ID, note, "", crew.DaemonID, garden.NoteKindNote, nil); err != nil {
		d.logf("harvest-on-merge: noting the cleared condition on %s: %v", seed.ID, err)
	}
	return nil
}

// The title is what a reader recognizes, so it is the part that gets trimmed to
// fit garden's harvest reason limit.
func harvestWhenReason(row store.SessionPullRequestRecord) string {
	head := fmt.Sprintf("PR #%d merged", row.Number)
	title := strings.TrimSpace(row.Title)
	if title == "" {
		return head
	}
	head += ": "
	room := garden.MaxReasonChars - utf8.RuneCountInString(head)
	if utf8.RuneCountInString(title) > room {
		title = string([]rune(title)[:room-1]) + "…"
	}
	return head + title
}
