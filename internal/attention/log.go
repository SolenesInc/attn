package attention

import (
	"encoding/json"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const DecisionLogFile = "attention-decisions.jsonl"

const (
	LogVerdict        = "verdict"
	LogSurfaced       = "surfaced"
	LogFollowUp       = "follow_up"
	LogNotice         = "notice"
	LogWaitEnded      = "wait_ended"
	LogNoticeDeadline = "notice_deadline"
)

type LogEntry struct {
	Kind        string             `json:"kind"`
	At          time.Time          `json:"at"`
	Session     protocol.SessionID `json:"session"`
	To          protocol.SessionID `json:"to,omitempty"`
	Checkpoint  Checkpoint         `json:"checkpoint,omitempty"`
	Subjects    []string           `json:"subjects,omitempty"`
	Key         string             `json:"key,omitempty"`
	Stop        time.Time          `json:"stop,omitzero"`
	WaitSince   time.Time          `json:"wait_since,omitzero"`
	SilentSince time.Time          `json:"silent_since,omitzero"`
	Verdict     Kind               `json:"verdict,omitempty"`
	Decider     string             `json:"decider,omitempty"`
	Label       Label              `json:"label,omitempty"`
	Reason      string             `json:"reason,omitempty"`
	Failure     Failure            `json:"failure,omitempty"`
	LatencyMs   int64              `json:"latency_ms,omitempty"`
	VerdictAt   *time.Time         `json:"verdict_at,omitempty"`
	FollowUp    string             `json:"follow_up,omitempty"`
	Facts       json.RawMessage    `json:"facts,omitempty"`
}
