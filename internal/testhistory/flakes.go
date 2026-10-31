package testhistory

import "sort"

type FailureRecord struct {
	Unit          string   `json:"unit"`
	Lane          string   `json:"lane"`
	FailedCommits int      `json:"failed_commits"`
	FlakyCommits  int      `json:"flaky_commits"`
	LastFailure   string   `json:"last_failure"`
	LastFlaky     bool     `json:"last_failure_also_passed"`
	PassesSince   int      `json:"passes_since"`
	Cases         []string `json:"cases,omitempty"`
	Receipts      []string `json:"receipts"`
}

func (h Health) FailureRecords() []FailureRecord {
	var records []FailureRecord
	for unit, lanes := range h.units {
		for lane, revisions := range lanes {
			if !failedSomewhere(revisions) {
				continue
			}
			standing, _ := h.laneStanding(lane, revisions)
			record := FailureRecord{Unit: unit, Lane: lane, LastFailure: standing.Anchor.Commit, PassesSince: standing.PassesSince, Cases: standing.Anchor.Cases, Receipts: standing.Anchor.Evidence,
				LastFlaky: revisions[standing.Anchor.Position].alsoPassed()}
			for _, outcome := range revisions {
				if !outcome.failed() {
					continue
				}
				record.FailedCommits++
				if outcome.alsoPassed() {
					record.FlakyCommits++
				}
			}
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.FlakyCommits != b.FlakyCommits {
			return a.FlakyCommits > b.FlakyCommits
		}
		if a.FailedCommits != b.FailedCommits {
			return a.FailedCommits > b.FailedCommits
		}
		if a.Unit != b.Unit {
			return a.Unit < b.Unit
		}
		return a.Lane < b.Lane
	})
	return records
}

func (o *revisionOutcome) alsoPassed() bool {
	var cases []string
	for name := range o.failedCases {
		if name != "" {
			cases = append(cases, name)
		}
	}
	return covers(o, cases)
}
