package testhistory

import (
	"fmt"
	"strings"
)

type UnitLine struct {
	Unit    string
	Outcome Outcome
	Missing bool
	Text    string
}

func (line UnitLine) Settled() bool {
	return !line.Missing && (line.Outcome == Pass || line.Outcome == Skip)
}

func Summarize(receipt Receipt) []UnitLine {
	unitResults := map[string][]Result{}
	var observedUnits []string
	for _, result := range receipt.Results {
		if result.Case != "" {
			continue
		}
		if _, seen := unitResults[result.Unit]; !seen {
			observedUnits = append(observedUnits, result.Unit)
		}
		unitResults[result.Unit] = append(unitResults[result.Unit], result)
	}
	units := receipt.Command.RequestedUnits
	if len(units) == 0 {
		units = observedUnits
	}

	lines := make([]UnitLine, 0, len(units))
	for _, unit := range units {
		results := unitResults[unit]
		if len(results) == 0 {
			lines = append(lines, UnitLine{Unit: unit, Missing: true, Text: "MISSING " + unit + " - " + missingReason(unit, unitResults)})
			continue
		}
		outcomes := make([]Outcome, len(results))
		var elapsed int64
		cached := false
		var detail string
		for i, result := range results {
			outcomes[i] = result.Outcome
			if result.ElapsedMS != nil {
				elapsed += *result.ElapsedMS
			}
			cached = cached || result.Cached
			if result.Detail != "" {
				detail = result.Detail
			}
		}
		outcome := aggregate(outcomes)
		text := fmt.Sprintf("%s %s (%.1fs)", strings.ToUpper(string(outcome)), unit, float64(elapsed)/1000)
		if cached {
			text += " (cached)"
		}
		if detail != "" && outcome != Pass {
			text += " - " + detail
		}
		lines = append(lines, UnitLine{Unit: unit, Outcome: outcome, Text: text})
	}
	return lines
}

func missingReason(unit string, unitResults map[string][]Result) string {
	fields := strings.Fields(unit)
	if len(fields) == 3 && fields[0] == string(RunnerGo) {
		for _, result := range unitResults["go "+fields[1]] {
			switch result.Outcome {
			case BuildFailure:
				return "its package did not build"
			case Fail:
				return "its package failed before reporting it"
			case Interrupted:
				return "its package was interrupted"
			}
		}
	}
	return "did not report a result: renamed, removed, or not run"
}
