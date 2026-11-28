package attention

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type Facts struct {
	Checkpoint           Checkpoint            `json:"checkpoint"`
	State                protocol.SessionState `json:"state"`
	InStateFor           Age                   `json:"in_state_for"`
	RunStartedBy         RunStarter            `json:"run_started_by"`
	SinceModelRequest    *Age                  `json:"since_model_request,omitempty"`
	TurnOwed             bool                  `json:"turn_owed"`
	ReportUnread         bool                  `json:"report_unread"`
	UserFocusedAfterStop bool                  `json:"user_focused_after_stop"`
	OpenTool             *Tool                 `json:"open_tool,omitempty"`
	Background           Background            `json:"background"`
	Wakes                []Wake                `json:"wakes,omitempty"`
	Delegates            []Delegate            `json:"delegates,omitempty"`
	Launch               Launch                `json:"launch"`
	ResultGoesTo         Recipient             `json:"result_goes_to"`
	Stake                *Seed                 `json:"stake,omitempty"`
	Tended               []Seed                `json:"tended,omitempty"`
	WatchedPRs           []PR                  `json:"watched_prs,omitempty"`
	DaemonDownFor        *Age                  `json:"daemon_down_for,omitempty"`
	LastMessage          string                `json:"last_message,omitempty"`
}

type Checkpoint string

const (
	CheckpointStop        Checkpoint = "stop"
	CheckpointFactChange  Checkpoint = "fact_change"
	CheckpointElapsed20m  Checkpoint = "elapsed_20m"
	CheckpointElapsed60m  Checkpoint = "elapsed_60m"
	CheckpointElapsed4h   Checkpoint = "elapsed_4h"
	CheckpointDaemonStart Checkpoint = "daemon_start"
)

func (c Checkpoint) Elapsed() bool {
	switch c {
	case CheckpointElapsed20m, CheckpointElapsed60m, CheckpointElapsed4h:
		return true
	default:
		return false
	}
}

type Background struct {
	Reported bool
	Tasks    []Task
}

func (b Background) MarshalJSON() ([]byte, error) {
	if !b.Reported {
		return json.Marshal("unknown")
	}
	return json.Marshal(append([]Task{}, b.Tasks...))
}

func (b Background) None() bool {
	return b.Reported && len(b.Tasks) == 0
}

func (f Facts) Fingerprint() string {
	f.Checkpoint = ""
	f.InStateFor = 0
	f.SinceModelRequest = nil
	f.DaemonDownFor = nil
	f.RunStartedBy = ""
	f.Background = Background{}
	if f.OpenTool != nil {
		tool := *f.OpenTool
		tool.OpenFor = 0
		f.OpenTool = &tool
	}
	f.Wakes = slices.Clone(f.Wakes)
	for i := range f.Wakes {
		f.Wakes[i].In, f.Wakes[i].Waited = nil, nil
	}
	encoded, _ := json.Marshal(f)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:12])
}

// 99% of 4,357 assistant messages across the 300 newest real transcripts fit in 3,575 characters, and 99% of
// the 710 answers to attn inbox rings in them fit in 3,044 (scripts on s-vn2wp7).
const LastMessageMaxChars = 4000

type Age time.Duration

func (a Age) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(a).Round(time.Second).String())
}

func AgeOf(from, now time.Time) Age {
	return Age(max(now.Sub(from), 0))
}

type RunStarter string

const (
	RunStartedByUser         RunStarter = "user"
	RunStartedByNotification RunStarter = "notification"
)

type Launch string

const (
	LaunchedByUser       Launch = "user"
	LaunchedByDelegation Launch = "delegation"
)

type Recipient string

const (
	ResultToUser       Recipient = "user"
	ResultToDispatcher Recipient = "dispatcher"
	ResultToNobody     Recipient = "nobody"
)

type Tool struct {
	Reason  string `json:"reason,omitempty"`
	OpenFor Age    `json:"open_for"`
}

type Task struct {
	ID          string `json:"id,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
	Name        string `json:"name,omitempty"`
	Server      string `json:"server,omitempty"`
	Tool        string `json:"tool,omitempty"`
	WakesAgent  string `json:"wakes_agent,omitempty"`
	Age         Age    `json:"age"`
}

type WakeKind string

const (
	WakeWatchedSeed WakeKind = "watched_seed"
	WakeAsk         WakeKind = "ask"
	WakeStandby     WakeKind = "standby"
	WakeReminder    WakeKind = "reminder"
	WakeSnooze      WakeKind = "snooze"
)

type Wake struct {
	Kind          WakeKind `json:"kind"`
	Seed          string   `json:"seed,omitempty"`
	SeedStatus    string   `json:"seed_status,omitempty"`
	TenderLive    *bool    `json:"tender_live,omitempty"`
	Subject       string   `json:"subject,omitempty"`
	SentSinceTurn bool     `json:"sent_since_turn,omitempty"`
	In            *Age     `json:"in,omitempty"`
	Waited        *Age     `json:"waited,omitempty"`
}

type Delegate struct {
	Session      protocol.SessionID    `json:"session"`
	Stake        string                `json:"stake,omitempty"`
	Live         bool                  `json:"live"`
	State        protocol.SessionState `json:"state"`
	OwesUserTurn bool                  `json:"owes_user_turn"`
	ReportsHere  bool                  `json:"reports_here"`
}

type Seed struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Status      string `json:"status"`
	ChecksDone  int    `json:"checks_done"`
	ChecksTotal int    `json:"checks_total"`
}

type PR struct {
	URL       string `json:"url"`
	State     string `json:"state"`
	CI        string `json:"ci,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}
