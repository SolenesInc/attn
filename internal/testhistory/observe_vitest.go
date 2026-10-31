package testhistory

import (
	"bufio"
	"bytes"
	"encoding/json"
)

type vitestRecord struct {
	Type       string   `json:"type"`
	Module     string   `json:"module"`
	Name       string   `json:"name"`
	State      string   `json:"state"`
	Mode       string   `json:"mode"`
	Fails      bool     `json:"fails"`
	RetryCount int      `json:"retryCount"`
	Duration   *float64 `json:"duration"`
	Errors     int      `json:"errors"`
	Reason     string   `json:"reason"`
}

func ObserveVitest(raw []byte) Observed {
	var observed Observed
	ranCase := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)
	for scanner.Scan() {
		var record vitestRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		switch record.Type {
		case "case":
			results := vitestCase(record)
			if final := results[len(results)-1].Outcome; final == Pass || final == ExpectedFailure {
				ranCase[record.Module] = true
			}
			observed.Results = append(observed.Results, results...)
		case "module":
			result := Result{Unit: "vitest " + record.Module, Attempt: 1}
			switch {
			case record.State == "failed" || record.Errors > 0:
				result.Outcome = Fail
			case record.State == "passed" && ranCase[record.Module]:
				result.Outcome = Pass
			case record.State == "passed" || record.State == "skipped":
				result.Outcome = Skip
			default:
				result.Outcome = Interrupted
			}
			if record.Duration != nil {
				result.ElapsedMS = milliseconds(*record.Duration)
			}
			observed.Results = append(observed.Results, result)
		case "end":
			observed.Finished = true
		}
	}
	return observed
}

func vitestCase(record vitestRecord) []Result {
	unit := "vitest " + record.Module
	final := Result{Unit: unit, Case: record.Name, Attempt: record.RetryCount + 1}
	switch record.State {
	case "passed":
		final.Outcome = Pass
		if record.Fails {
			final.Outcome = ExpectedFailure
		}
	case "failed":
		final.Outcome = Fail
	case "skipped":
		final.Outcome = Skip
		if record.Mode == "todo" {
			final.Detail = "todo"
		}
	default:
		final.Outcome = Interrupted
	}
	if record.Duration != nil {
		final.ElapsedMS = milliseconds(*record.Duration)
	}
	results := make([]Result, 0, record.RetryCount+1)
	for attempt := 1; attempt <= record.RetryCount; attempt++ {
		results = append(results, Result{Unit: unit, Case: record.Name, Attempt: attempt, Outcome: Fail, Detail: "retried"})
	}
	return append(results, final)
}
