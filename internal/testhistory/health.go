package testhistory

import (
	"fmt"
	"sort"
	"strings"
)

type Mainline struct {
	commits   []string
	positions map[string]int
}

func NewMainline(oldestFirst []string) Mainline {
	positions := make(map[string]int, len(oldestFirst))
	for i, commit := range oldestFirst {
		positions[commit] = i
	}
	return Mainline{commits: oldestFirst, positions: positions}
}

func (m Mainline) Position(commit string) (int, bool) {
	position, ok := m.positions[commit]
	return position, ok
}

func (m Mainline) Commit(position int) string { return m.commits[position] }
func (m Mainline) Len() int                   { return len(m.commits) }

func (m Mainline) Before(commit string) Mainline {
	position, ok := m.positions[commit]
	if !ok {
		return m
	}
	return NewMainline(m.commits[:position])
}

func (l Lane) String() string {
	lane := l.OS + "/" + l.Arch
	if l.Race {
		lane += "/race"
	}
	return lane
}

type revisionOutcome struct {
	failedCases map[string]bool
	passedCases map[string]bool
	failedIn    []string
}

func (o *revisionOutcome) failed() bool { return len(o.failedCases) > 0 }

type Health struct {
	main  Mainline
	units map[string]map[string]map[int]*revisionOutcome
}

func HealthEvidence(receipt Receipt, main Mainline) (int, bool) {
	if receipt.Revision.DirtyFingerprint != "" {
		return 0, false
	}
	return main.Position(receipt.Revision.Commit)
}

func BuildHealth(receipts []Receipt, main Mainline) Health {
	health := Health{main: main, units: map[string]map[string]map[int]*revisionOutcome{}}
	for _, receipt := range receipts {
		position, ok := HealthEvidence(receipt, main)
		if !ok {
			continue
		}
		lane := receipt.Lane.String()
		for _, result := range receipt.Results {
			if !schedulable(result.Unit) {
				continue
			}
			outcome := health.outcome(result.Unit, lane, position)
			switch {
			case result.Outcome == Fail:
				outcome.failedCases[result.Case] = true
				outcome.failedIn = appendOnce(outcome.failedIn, receipt.InvocationID)
			case result.Outcome == Pass && !result.Cached:
				outcome.passedCases[result.Case] = true
			}
		}
	}
	return health
}

func schedulable(unit string) bool {
	return UnitRunner(unit) != RunnerGo || len(strings.Fields(unit)) == 3
}

func appendOnce(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

func (h Health) outcome(unit, lane string, position int) *revisionOutcome {
	lanes := h.units[unit]
	if lanes == nil {
		lanes = map[string]map[int]*revisionOutcome{}
		h.units[unit] = lanes
	}
	revisions := lanes[lane]
	if revisions == nil {
		revisions = map[int]*revisionOutcome{}
		lanes[lane] = revisions
	}
	outcome := revisions[position]
	if outcome == nil {
		outcome = &revisionOutcome{failedCases: map[string]bool{}, passedCases: map[string]bool{}}
		revisions[position] = outcome
	}
	return outcome
}

type AnchorKind string

const (
	AnchorFailure   AnchorKind = "failure"
	AnchorAdmission AnchorKind = "baseline-admission"
)

type Anchor struct {
	Kind     AnchorKind `json:"kind"`
	Commit   string     `json:"commit"`
	Position int        `json:"position"`
	Cases    []string   `json:"cases,omitempty"`
	Evidence []string   `json:"evidence,omitempty"`
}

type Admission struct {
	Commit   string
	Position int
}

func (m Mainline) Admission(commit string) Admission {
	if position, ok := m.Position(commit); ok {
		return Admission{Commit: commit, Position: position}
	}
	return Admission{Commit: commit, Position: m.Len()}
}

type LaneStanding struct {
	Lane          string   `json:"lane"`
	Anchor        Anchor   `json:"anchor"`
	PassesSince   int      `json:"passes_since"`
	PassRevisions []string `json:"pass_revisions,omitempty"`
}

type Standing struct {
	LaneStanding
	Lanes []LaneStanding `json:"lanes,omitempty"`
}

func (h Health) Standing(unit string, admission *Admission) (Standing, bool) {
	var lanes []LaneStanding
	for lane, revisions := range h.units[unit] {
		if standing, ok := h.laneStanding(lane, revisions, admission); ok {
			lanes = append(lanes, standing)
		}
	}
	if len(lanes) == 0 {
		if admission == nil {
			return Standing{}, false
		}
		return Standing{LaneStanding: LaneStanding{Anchor: admissionAnchor(*admission)}}, true
	}
	sort.Slice(lanes, func(i, j int) bool { return lessCleared(lanes[i], lanes[j]) })
	return Standing{LaneStanding: lanes[0], Lanes: lanes}, true
}

func lessCleared(a, b LaneStanding) bool {
	if a.PassesSince != b.PassesSince {
		return a.PassesSince < b.PassesSince
	}
	if a.Anchor.Position != b.Anchor.Position {
		return a.Anchor.Position > b.Anchor.Position
	}
	return a.Lane < b.Lane
}

func admissionAnchor(admission Admission) Anchor {
	return Anchor{Kind: AnchorAdmission, Commit: admission.Commit, Position: admission.Position}
}

func (h Health) laneStanding(lane string, revisions map[int]*revisionOutcome, admission *Admission) (LaneStanding, bool) {
	latestFailure := -1
	for position, outcome := range revisions {
		if outcome.failed() && position > latestFailure {
			latestFailure = position
		}
	}
	var anchor Anchor
	switch {
	case admission != nil && admission.Position > latestFailure:
		anchor = admissionAnchor(*admission)
	case latestFailure >= 0:
		failure := revisions[latestFailure]
		anchor = Anchor{Kind: AnchorFailure, Commit: h.main.Commit(latestFailure), Position: latestFailure, Evidence: append([]string(nil), failure.failedIn...)}
		for name := range failure.failedCases {
			if name != "" {
				anchor.Cases = append(anchor.Cases, name)
			}
		}
		sort.Strings(anchor.Cases)
		sort.Strings(anchor.Evidence)
	default:
		return LaneStanding{}, false
	}
	var passes []int
	for position, outcome := range revisions {
		if position > anchor.Position && covers(outcome, anchor.Cases) {
			passes = append(passes, position)
		}
	}
	sort.Ints(passes)
	standing := LaneStanding{Lane: lane, Anchor: anchor, PassesSince: len(passes)}
	for _, position := range passes {
		standing.PassRevisions = append(standing.PassRevisions, h.main.Commit(position))
	}
	return standing, true
}

func covers(outcome *revisionOutcome, cases []string) bool {
	if !outcome.passedCases[""] {
		return false
	}
	for _, name := range cases {
		if !outcome.passedCases[name] {
			return false
		}
	}
	return true
}

func (h Health) FailedAt(position int) map[string][]string {
	failed := map[string][]string{}
	for unit, lanes := range h.units {
		for lane, revisions := range lanes {
			if outcome := revisions[position]; outcome != nil && outcome.failed() {
				failed[unit] = append(failed[unit], lane)
			}
		}
	}
	for unit := range failed {
		sort.Strings(failed[unit])
	}
	return failed
}

func (h Health) FailureUnits() []string {
	var units []string
	for unit, lanes := range h.units {
		for _, revisions := range lanes {
			if failedSomewhere(revisions) {
				units = append(units, unit)
				break
			}
		}
	}
	sort.Strings(units)
	return units
}

func failedSomewhere(revisions map[int]*revisionOutcome) bool {
	for _, outcome := range revisions {
		if outcome.failed() {
			return true
		}
	}
	return false
}

func (s LaneStanding) Describe() string {
	switch s.Anchor.Kind {
	case AnchorAdmission:
		return fmt.Sprintf("admitted to the baseline at %s, %d passing main revisions since", short(s.Anchor.Commit), s.PassesSince)
	default:
		cases := ""
		if len(s.Anchor.Cases) > 0 {
			cases = " (" + strings.Join(s.Anchor.Cases, ", ") + ")"
		}
		return fmt.Sprintf("failed on %s at %s%s, %d passing main revisions since", s.Lane, short(s.Anchor.Commit), cases, s.PassesSince)
	}
}

func short(commit string) string {
	if len(commit) > 9 {
		return commit[:9]
	}
	return commit
}
