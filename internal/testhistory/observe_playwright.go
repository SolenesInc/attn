package testhistory

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

type playwrightReport struct {
	Config struct {
		RootDir    string `json:"rootDir"`
		ConfigFile string `json:"configFile"`
	} `json:"config"`
	Suites []playwrightSuite `json:"suites"`
	Errors []struct {
		Location *struct {
			File string `json:"file"`
		} `json:"location"`
	} `json:"errors"`
}

type playwrightSuite struct {
	Title  string            `json:"title"`
	File   string            `json:"file"`
	Specs  []playwrightSpec  `json:"specs"`
	Suites []playwrightSuite `json:"suites"`
}

type playwrightSpec struct {
	Title string `json:"title"`
	File  string `json:"file"`
	Tests []struct {
		ProjectName    string `json:"projectName"`
		ExpectedStatus string `json:"expectedStatus"`
		Annotations    []struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"annotations"`
		Results []struct {
			Retry    int     `json:"retry"`
			Status   string  `json:"status"`
			Duration float64 `json:"duration"`
		} `json:"results"`
	} `json:"tests"`
}

func ObservePlaywright(raw []byte) Observed {
	var report playwrightReport
	if json.Unmarshal(raw, &report) != nil {
		return Observed{}
	}
	appDir := filepath.Dir(report.Config.ConfigFile)
	unitOf := func(file string) string {
		if !filepath.IsAbs(file) {
			file = filepath.Join(report.Config.RootDir, file)
		}
		relative, err := filepath.Rel(appDir, file)
		if err != nil {
			relative = file
		}
		return "playwright " + filepath.ToSlash(relative)
	}

	observed := Observed{Finished: true}
	type unitSummary struct {
		finals  []Outcome
		elapsed float64
	}
	var units []string
	summaries := map[string]*unitSummary{}
	summaryOf := func(unit string) *unitSummary {
		if summaries[unit] == nil {
			summaries[unit] = &unitSummary{}
			units = append(units, unit)
		}
		return summaries[unit]
	}

	var cases []Result
	var walk func(suite playwrightSuite, titles []string)
	walk = func(suite playwrightSuite, titles []string) {
		for _, spec := range suite.Specs {
			unit := unitOf(spec.File)
			summary := summaryOf(unit)
			for _, test := range spec.Tests {
				path := append(append([]string{}, titles...), spec.Title)
				if test.ProjectName != "" {
					path = append([]string{test.ProjectName}, path...)
				}
				name := strings.Join(path, " › ")
				for _, attempt := range test.Results {
					result := Result{Unit: unit, Case: name, Attempt: attempt.Retry + 1, ElapsedMS: milliseconds(attempt.Duration)}
					result.Outcome, result.Detail = playwrightOutcome(attempt.Status, test.ExpectedStatus)
					if result.Outcome == Skip {
						for _, annotation := range test.Annotations {
							if annotation.Type == "skip" || annotation.Type == "fixme" {
								result.Detail = strings.TrimSpace(annotation.Type + ": " + annotation.Description)
							}
						}
					}
					summary.elapsed += attempt.Duration
					cases = append(cases, result)
				}
				if len(test.Results) > 0 {
					summary.finals = append(summary.finals, cases[len(cases)-1].Outcome)
				}
			}
		}
		for _, child := range suite.Suites {
			walk(child, append(append([]string{}, titles...), child.Title))
		}
	}
	for _, suite := range report.Suites {
		summaryOf(unitOf(suite.File))
		walk(suite, nil)
	}
	failedToLoad := map[string]bool{}
	for _, failure := range report.Errors {
		if failure.Location != nil && failure.Location.File != "" {
			unit := unitOf(failure.Location.File)
			summaryOf(unit)
			failedToLoad[unit] = true
		}
	}

	for _, unit := range units {
		summary := summaries[unit]
		result := Result{Unit: unit, Attempt: 1, ElapsedMS: milliseconds(summary.elapsed), Outcome: Skip}
		if failedToLoad[unit] {
			result.Outcome, result.Detail, result.ElapsedMS = Fail, "failed to load", nil
		} else if len(summary.finals) == 0 {
			continue
		} else {
			result.Outcome = aggregate(summary.finals)
		}
		observed.Results = append(observed.Results, result)
	}
	observed.Results = append(observed.Results, cases...)
	return observed
}

func playwrightOutcome(status, expected string) (Outcome, string) {
	switch status {
	case "skipped":
		return Skip, ""
	case "interrupted":
		return Interrupted, ""
	case "passed":
		if expected == "failed" {
			return Fail, "expected to fail but passed"
		}
		return Pass, ""
	case "timedOut":
		return Fail, "timed out"
	case "failed":
		if expected == "failed" {
			return ExpectedFailure, ""
		}
		return Fail, ""
	default:
		return Fail, status
	}
}

func aggregate(finals []Outcome) Outcome {
	outcome := Skip
	for _, final := range finals {
		switch {
		case final == Fail || final == BuildFailure:
			return Fail
		case final == Interrupted:
			outcome = Interrupted
		case (final == Pass || final == ExpectedFailure) && outcome == Skip:
			outcome = Pass
		}
	}
	return outcome
}
