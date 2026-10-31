package testhistory

import (
	"encoding/json"
	"fmt"
)

type nativeMatrix struct {
	Complete bool `json:"complete"`
	Results  []struct {
		ID          string   `json:"id"`
		Code        int      `json:"code"`
		DurationMS  *float64 `json:"durationMs"`
		Skipped     bool     `json:"skipped"`
		SkipReason  string   `json:"skipReason"`
		TimedOut    bool     `json:"timedOut"`
		Signal      string   `json:"signal"`
		Interrupted bool     `json:"interrupted"`
	} `json:"results"`
}

func ObserveNative(raw []byte) Observed {
	var matrix nativeMatrix
	if json.Unmarshal(raw, &matrix) != nil {
		return Observed{}
	}
	observed := Observed{Finished: matrix.Complete}
	for _, scenario := range matrix.Results {
		result := Result{Unit: "native " + scenario.ID, Attempt: 1}
		switch {
		case scenario.Skipped:
			result.Outcome, result.Detail = Skip, scenario.SkipReason
		case scenario.Interrupted:
			result.Outcome = Interrupted
		case scenario.Code == 0:
			result.Outcome = Pass
		case scenario.TimedOut:
			result.Outcome, result.Detail = Fail, "timed out"
		case scenario.Signal != "":
			result.Outcome, result.Detail = Fail, "killed by "+scenario.Signal
		default:
			result.Outcome, result.Detail = Fail, fmt.Sprintf("exit %d", scenario.Code)
		}
		if scenario.DurationMS != nil && !scenario.Skipped {
			result.ElapsedMS = milliseconds(*scenario.DurationMS)
		}
		observed.Results = append(observed.Results, result)
	}
	return observed
}
