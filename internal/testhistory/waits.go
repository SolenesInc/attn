package testhistory

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

const (
	FixedWaitLatestPasses = 20
	FixedWaitMinPasses    = 5
	FixedWaitFloorMS      = 15_000
	FixedWaitSpreadPct    = 5
)

type FixedWait struct {
	Lane      string `json:"lane"`
	Unit      string `json:"unit"`
	Passes    int    `json:"passes"`
	MedianMS  int64  `json:"median_ms"`
	FastestMS int64  `json:"fastest_ms"`
	SlowestMS int64  `json:"slowest_ms"`
}

func FixedWaits(receipts []Receipt, main Mainline) []FixedWait {
	type laneUnit struct{ lane, unit string }
	type slot struct{ lane, job, shard string }
	type pass struct {
		slot    slot
		started time.Time
		ms      int64
	}
	passes := map[laneUnit][]pass{}
	latestRun := map[slot]time.Time{}
	for _, receipt := range receipts {
		if _, onMain := HealthEvidence(receipt, main); !onMain || !receipt.Complete || receipt.Runner != RunnerGo || receipt.Source.Kind != "ci" {
			continue
		}
		lane := receipt.Lane.String()
		ran := slot{lane, receipt.Source.Job, receipt.Source.Shard}
		if receipt.Command.StartedAt.After(latestRun[ran]) {
			latestRun[ran] = receipt.Command.StartedAt
		}
		for _, result := range receipt.Results {
			if result.Outcome != Pass || result.Cached || result.Case != "" || result.ElapsedMS == nil || len(strings.Fields(result.Unit)) != 3 {
				continue
			}
			key := laneUnit{lane, result.Unit}
			passes[key] = append(passes[key], pass{ran, receipt.Command.StartedAt, *result.ElapsedMS})
		}
	}
	var waits []FixedWait
	for key, all := range passes {
		slices.SortFunc(all, func(a, b pass) int { return b.started.Compare(a.started) })
		if latest := all[0]; !latest.started.Equal(latestRun[latest.slot]) {
			continue
		}
		var elapsed []int64
		for _, p := range all[:min(len(all), FixedWaitLatestPasses)] {
			elapsed = append(elapsed, p.ms)
		}
		slices.Sort(elapsed)
		fastest, median := elapsed[0], elapsed[len(elapsed)/2]
		if len(elapsed) < FixedWaitMinPasses || fastest < FixedWaitFloorMS || median*100 > fastest*(100+FixedWaitSpreadPct) {
			continue
		}
		waits = append(waits, FixedWait{Lane: key.lane, Unit: key.unit, Passes: len(elapsed),
			MedianMS: median, FastestMS: fastest, SlowestMS: elapsed[len(elapsed)-1]})
	}
	slices.SortFunc(waits, func(a, b FixedWait) int {
		return cmp.Or(cmp.Compare(b.MedianMS, a.MedianMS), strings.Compare(a.Unit, b.Unit), strings.Compare(a.Lane, b.Lane))
	})
	return waits
}
