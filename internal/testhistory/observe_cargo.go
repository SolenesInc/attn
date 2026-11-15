package testhistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const CargoSuiteUnit = "cargo ."

type cargoMessage struct {
	Reason     string      `json:"reason"`
	PackageID  string      `json:"package_id"`
	Target     cargoTarget `json:"target"`
	Executable string      `json:"executable"`
	Success    *bool       `json:"success"`
	Message    struct {
		Level string `json:"level"`
	} `json:"message"`
}

type cargoTarget struct {
	Name string   `json:"name"`
	Kind []string `json:"kind"`
}

var (
	cargoRunning    = regexp.MustCompile(`^\s+Running (?:unittests )?\S+ \((.+)\)$`)
	cargoDocTests   = regexp.MustCompile(`^\s+Doc-tests (\S+)$`)
	cargoTestLine   = regexp.MustCompile(`^test (.+) \.\.\. (ok|FAILED|ignored(?:, (.*))?)$`)
	cargoTestResult = regexp.MustCompile(`^test result: (ok|FAILED)\..* finished in ([0-9.]+)s$`)
	cargoBinaryDied = regexp.MustCompile(`^error: test failed, to rerun pass`)
)

const cargoBinaryDiedDetail = "the test binary exited before reporting its result"

func ObserveCargo(raw []byte) Observed {
	var observed Observed
	executables := map[string]string{}
	libraries := map[string]string{}
	running := ""
	built := false
	buildFailures := map[string]bool{}
	settle := func(outcome Outcome, detail string) {
		if running != "" {
			observed.Results = append(observed.Results, Result{Unit: running, Attempt: 1, Outcome: outcome, Detail: detail})
			running = ""
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)
	for scanner.Scan() {
		line := scanner.Text()
		var message cargoMessage
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &message) == nil && message.Reason != "" {
			pkg := cargoPackageName(message.PackageID)
			switch {
			case message.Reason == "compiler-artifact" && message.Executable != "":
				executables[path.Base(message.Executable)] = cargoUnit(pkg, message.Target)
			case message.Reason == "compiler-artifact" && hasKind(message.Target, "lib"):
				libraries[message.Target.Name] = pkg
			case message.Reason == "compiler-message" && message.Message.Level == "error":
				if unit := cargoUnit(pkg, message.Target); !buildFailures[unit] {
					buildFailures[unit] = true
					observed.Results = append(observed.Results, Result{Unit: unit, Attempt: 1, Outcome: BuildFailure})
				}
			case message.Reason == "build-finished":
				built = true
			}
			continue
		}
		if cargoBinaryDied.MatchString(line) {
			settle(Fail, cargoBinaryDiedDetail)
			continue
		}
		if match := cargoRunning.FindStringSubmatch(line); match != nil {
			settle(Fail, cargoBinaryDiedDetail)
			running = executables[path.Base(match[1])]
			continue
		}
		if match := cargoDocTests.FindStringSubmatch(line); match != nil {
			settle(Fail, cargoBinaryDiedDetail)
			if pkg, ok := libraries[match[1]]; ok {
				running = "cargo " + pkg + " --doc"
			}
			continue
		}
		if running == "" {
			continue
		}
		if match := cargoTestLine.FindStringSubmatch(line); match != nil {
			result := Result{Unit: running, Case: match[1], Attempt: 1}
			switch match[2] {
			case "ok":
				result.Outcome = Pass
			case "FAILED":
				result.Outcome = Fail
			default:
				result.Outcome, result.Detail = Skip, match[3]
			}
			observed.Results = append(observed.Results, result)
			continue
		}
		if match := cargoTestResult.FindStringSubmatch(line); match != nil {
			unit := running
			running = ""
			result := Result{Unit: unit, Attempt: 1, Outcome: Pass}
			if match[1] == "FAILED" {
				result.Outcome = Fail
			}
			if elapsed, err := strconv.ParseFloat(match[2], 64); err == nil {
				result.ElapsedMS = milliseconds(elapsed * 1000)
			}
			observed.Results = append(observed.Results, result)
		}
	}
	observed.Finished = built && running == ""
	settle(Interrupted, "")
	return observed
}

func cargoUnit(pkg string, target cargoTarget) string {
	switch {
	case hasKind(target, "test"):
		return "cargo " + pkg + " --test " + target.Name
	case hasKind(target, "bin"):
		return "cargo " + pkg + " --bin " + target.Name
	case hasKind(target, "example"):
		return "cargo " + pkg + " --example " + target.Name
	case hasKind(target, "bench"):
		return "cargo " + pkg + " --bench " + target.Name
	default:
		return "cargo " + pkg + " --lib"
	}
}

func hasKind(target cargoTarget, kind string) bool {
	for _, k := range target.Kind {
		if k == kind || strings.HasSuffix(k, "lib") && kind == "lib" {
			return true
		}
	}
	return false
}

func cargoPackageName(id string) string {
	if name, _, ok := strings.Cut(id, " "); ok && !strings.Contains(name, "://") {
		return name
	}
	location, fragment, _ := strings.Cut(id, "#")
	if name, _, ok := strings.Cut(fragment, "@"); ok {
		return name
	}
	return path.Base(strings.TrimSuffix(location, "/"))
}
