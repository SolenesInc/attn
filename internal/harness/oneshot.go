package harness

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// OneShot is the optional part of a harness that answers one prompt without a session.
type OneShot interface {
	Run(ctx context.Context, req OneShotRequest) (OneShotResult, error)
}

// OneShotRequest is one prompt for a harness to answer with its tools off.
type OneShotRequest struct {
	// Profile names the profile whose running server answers, for a harness that has one; "" runs the harness's own process.
	Profile string
	Model   string
	Effort  string
	System  string
	Input   string
	Schema  json.RawMessage
	// Dir is the folder the harness runs in; "" is a scratch folder of its own.
	Dir   string
	Bound Bound
}

// Bound is how far a one-shot run may go, for a harness that can bound it.
type Bound struct {
	MaxTurns   int
	MaxCostUSD string
}

type OneShotResult struct {
	Model         string
	Text          string
	Structured    json.RawMessage
	Diagnostics   string
	FailureOutput string
	Usage         OneShotUsage
}

// OneShotUsage is what a run cost, zero where the harness does not report it.
type OneShotUsage struct {
	In, Out   int
	CostUSD   float64
	ModelTime time.Duration
	Turns     int
}

// SpareStarter is the optional part of a harness that can start its process before a one-shot call exists.
type SpareStarter interface {
	StartSpare(ctx context.Context) (Spare, error)
}

// Spare answers one call; its process exits after Answer or Close.
type Spare interface {
	Answer(ctx context.Context, req OneShotRequest) (OneShotResult, error)
	Close()
}

// ErrSpareUnusable is returned by Spare.Answer when the spare failed before it received the call's input, so the call can still run cold.
var ErrSpareUnusable = errors.New("spare unusable")
