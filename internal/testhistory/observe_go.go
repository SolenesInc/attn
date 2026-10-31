package testhistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

type GoEvent struct {
	Action      string
	Package     string
	Test        string
	Elapsed     *float64
	Output      string
	OutputType  string
	FailedBuild string
	ImportPath  string
}

func ParseGoEvent(line []byte) (GoEvent, bool) {
	var event GoEvent
	if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &event) != nil || event.Action == "" {
		return GoEvent{}, false
	}
	return event, true
}

func goTerminal(action string) bool {
	return action == "pass" || action == "fail" || action == "skip"
}

type goRun struct {
	name    string
	attempt int
	action  string
	elapsed *float64
	said    string
}

type goPackage struct {
	path   string
	ended  bool
	action string
	build  bool
	cached bool
	halted bool
	elapse *float64
	runs   []*goRun
	open   map[string]*goRun
	count  map[string]int
}

func ObserveGo(raw []byte, module string) Observed {
	var order []*goPackage
	packages := map[string]*goPackage{}
	packageOf := func(importPath string) *goPackage {
		pkg := packages[importPath]
		if pkg == nil {
			pkg = &goPackage{path: GoPackagePath(importPath, module), open: map[string]*goRun{}, count: map[string]int{}}
			packages[importPath] = pkg
			order = append(order, pkg)
		}
		return pkg
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)
	for scanner.Scan() {
		event, ok := ParseGoEvent(scanner.Bytes())
		if !ok || event.Package == "" {
			continue
		}
		pkg := packageOf(event.Package)
		if event.Action == "output" && interruptedBinary(event.Output) {
			pkg.halted = true
		}
		if event.Test == "" {
			switch {
			case event.Action == "output" && strings.HasPrefix(event.Output, "ok  ") && strings.Contains(event.Output, "\t(cached)"):
				pkg.cached = true
			case goTerminal(event.Action):
				pkg.ended, pkg.action, pkg.elapse, pkg.build = true, event.Action, event.Elapsed, event.FailedBuild != ""
			}
			continue
		}
		switch {
		case event.Action == "run":
			pkg.count[event.Test]++
			run := &goRun{name: event.Test, attempt: pkg.count[event.Test]}
			pkg.open[event.Test] = run
			pkg.runs = append(pkg.runs, run)
		case event.Action == "output" && event.OutputType != "frame":
			if run := pkg.open[event.Test]; run != nil {
				if said := strings.TrimSpace(event.Output); said != "" {
					run.said = said
				}
			}
		case goTerminal(event.Action):
			if run := pkg.open[event.Test]; run != nil {
				run.action, run.elapsed = event.Action, event.Elapsed
				delete(pkg.open, event.Test)
			}
		}
	}

	observed := Observed{Finished: len(order) > 0}
	for _, pkg := range order {
		observed.Finished = observed.Finished && pkg.ended
		packageResult := Result{Unit: "go " + pkg.path, Attempt: 1, Cached: pkg.cached}
		switch {
		case !pkg.ended || (pkg.halted && pkg.action == "fail"):
			packageResult.Outcome = Interrupted
		case pkg.build:
			packageResult.Outcome = BuildFailure
		default:
			packageResult.Outcome = goOutcome(pkg.action)
		}
		if pkg.elapse != nil {
			packageResult.ElapsedMS = milliseconds(*pkg.elapse * 1000)
		}
		observed.Results = append(observed.Results, packageResult)

		for _, run := range pkg.runs {
			top, sub, _ := strings.Cut(run.name, "/")
			result := Result{Unit: "go " + pkg.path + " " + top, Case: sub, Attempt: run.attempt, Cached: pkg.cached}
			switch {
			case run.action != "":
				result.Outcome = goOutcome(run.action)
				if run.action == "skip" {
					result.Detail = run.said
				}
			case pkg.ended && !pkg.halted:
				result.Outcome = Fail
				result.Detail = "still running when its package ended"
			default:
				result.Outcome = Interrupted
			}
			if run.elapsed != nil {
				result.ElapsedMS = milliseconds(*run.elapsed * 1000)
			}
			observed.Results = append(observed.Results, result)
		}
	}
	return observed
}

func interruptedBinary(output string) bool {
	switch output {
	case "signal: interrupt\n", "signal: terminated\n", "signal: hangup\n":
		return true
	}
	return false
}

func goOutcome(action string) Outcome {
	switch action {
	case "pass":
		return Pass
	case "skip":
		return Skip
	default:
		return Fail
	}
}

func GoPackagePath(importPath, module string) string {
	if importPath == module {
		return "."
	}
	if rest, ok := strings.CutPrefix(importPath, module+"/"); ok {
		return "./" + rest
	}
	return importPath
}
