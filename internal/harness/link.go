// Package harness is the contract between attn and the agent harnesses it hosts.
package harness

import (
	"context"
	"time"
)

// Voice says whose words an input carries; a link declares the voices it can deliver.
type Voice uint8

const (
	// VoiceUser is the user's words: annotations and user conversation.
	VoiceUser Voice = iota
	// VoiceAttn is attn's own: rings, heartbeats, nudges, maintenance.
	VoiceAttn
)

// Input is one delivery to a session. ID names the attempt; a link may pass it on.
type Input struct {
	Session string
	ID      string
	Text    string
	Voice   Voice
}

// Custody is a delivery's only result: whether the harness took the input, and when.
type Custody struct {
	Taken  bool
	At     time.Time
	Reason string
}

// Link delivers input to a harness without going through its terminal.
type Link interface {
	Voices() []Voice
	Deliver(ctx context.Context, in Input) Custody
}
