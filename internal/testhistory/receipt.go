package testhistory

import (
	"fmt"
	"time"
)

const Schema = "attn.test-receipt/v1"

type Runner string

const (
	RunnerGo         Runner = "go"
	RunnerVitest     Runner = "vitest"
	RunnerPlaywright Runner = "playwright"
	RunnerNative     Runner = "native"
)

var runners = []Runner{RunnerGo, RunnerVitest, RunnerPlaywright, RunnerNative}

func ParseRunner(name string) (Runner, error) {
	for _, runner := range runners {
		if string(runner) == name {
			return runner, nil
		}
	}
	return "", fmt.Errorf("unknown runner %q; runners: go, vitest, playwright, native", name)
}

type Outcome string

const (
	Pass            Outcome = "pass"
	Fail            Outcome = "fail"
	Skip            Outcome = "skip"
	ExpectedFailure Outcome = "expected-failure"
	Interrupted     Outcome = "interrupted"
	BuildFailure    Outcome = "build-failure"
)

type Receipt struct {
	Schema       string      `json:"schema"`
	Repo         string      `json:"repo"`
	InvocationID string      `json:"invocation_id"`
	Runner       Runner      `json:"runner"`
	Source       Source      `json:"source"`
	Revision     Revision    `json:"revision"`
	Lane         Lane        `json:"lane"`
	Diagnostics  Diagnostics `json:"diagnostics"`
	Command      Command     `json:"command"`
	Results      []Result    `json:"results"`
	Complete     bool        `json:"complete"`
}

type Source struct {
	Kind    string `json:"kind"`
	RunID   string `json:"run_id,omitempty"`
	Attempt string `json:"attempt,omitempty"`
	Job     string `json:"job,omitempty"`
	Shard   string `json:"shard,omitempty"`
}

type Revision struct {
	Commit           string `json:"commit"`
	Tree             string `json:"tree"`
	DirtyFingerprint string `json:"dirty_fingerprint,omitempty"`
}

type Lane struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Race bool   `json:"race"`
}

type Diagnostics struct {
	MachineClass string            `json:"machine_class"`
	ToolVersions map[string]string `json:"tool_versions,omitempty"`
}

type Command struct {
	Argv           []string   `json:"argv"`
	RequestedUnits []string   `json:"requested_units,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Exit           *Exit      `json:"exit,omitempty"`
	WallMS         *int64     `json:"wall_ms,omitempty"`
	Interrupted    string     `json:"interrupted,omitempty"`
}

type Exit struct {
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
}

type Result struct {
	Unit      string  `json:"unit"`
	Case      string  `json:"case,omitempty"`
	Attempt   int     `json:"attempt"`
	Outcome   Outcome `json:"outcome"`
	ElapsedMS *int64  `json:"elapsed_ms,omitempty"`
	Cached    bool    `json:"cached,omitempty"`
	Detail    string  `json:"detail,omitempty"`
}

type Observed struct {
	Results  []Result
	Finished bool
}

func milliseconds(value float64) *int64 {
	ms := int64(value + 0.5)
	return &ms
}
