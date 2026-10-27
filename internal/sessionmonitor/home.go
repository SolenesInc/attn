package sessionmonitor

import (
	"context"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

type Home interface {
	Applied(View) (Answer, bool)
}

type SwitchOutcome string

const (
	SwitchBound   SwitchOutcome = "bound"
	SwitchOpened  SwitchOutcome = "opened"
	SwitchShows   SwitchOutcome = "shows"
	SwitchInPlace SwitchOutcome = "in_place"
	SwitchDropped SwitchOutcome = "dropped"
)

type Answer struct {
	Conversation string
	Outcome      SwitchOutcome
	Session      protocol.SessionID
	Reason       string
	Cost         *sessioncost.Checkpoint
}

type Policy struct {
	ChiefOfStaff       bool
	AutoSettle         AutoSettleSettings
	Headless           bool
	TitlesAllowed      bool
	DriverReportsState bool
	QuietNotifications bool
}

type Launch struct {
	Known              bool
	Agent              protocol.SessionAgent
	Profile            string
	Link               string
	StartedAt          time.Time
	Directory          string
	Branch             attngit.BranchInfo
	HarnessTitle       string
	State              protocol.SessionState
	StateSince         time.Time
	StateUpdated       time.Time
	LastModelRequestAt time.Time
	Turn               TurnStamps
	Driver             DriverMetadata
	Cost               sessioncost.Checkpoint
	Exit               *Exit
	Conversation       ConversationObservation
	LastSeen           time.Time
	PendingSwitch      []Conversation
	PullRequests       map[string]PullRequest
}

func (m *Monitor) SetPolicy(policy Policy) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.policy = policy
	m.mu.Unlock()
}

func (m *Monitor) Policy() Policy {
	if m == nil {
		return Policy{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy
}

func (m *Monitor) Bind(label string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.label = label
	m.mu.Unlock()
}

func (m *Monitor) Label() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.label
}

func (m *Monitor) logf(format string, args ...any) { m.registry.host.Logf(format, args...) }

func (m *Monitor) current() bool {
	m.registry.mu.Lock()
	defer m.registry.mu.Unlock()
	_, ok := m.registry.monitors[m]
	return ok
}

func (m *Monitor) Launched(at time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.startedAt = at
	m.mu.Unlock()
}

func (m *Monitor) Touch(at time.Time) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	m.touchedAt = at
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

type terminalViews struct {
	owner     string
	terminals []harness.TerminalID
}

func (m *Monitor) SetTerminals(owner string, terminals []harness.TerminalID) {
	if m == nil {
		return
	}
	m.views.Store(&terminalViews{owner: owner, terminals: terminals})
}

func (m *Monitor) terminalViews() terminalViews {
	if views := m.views.Load(); views != nil {
		return *views
	}
	return terminalViews{}
}

func (m *Monitor) primaryTerminalLocked() harness.TerminalID {
	if m.session == "" && m.conversationInput.Terminal != "" {
		return m.conversationInput.Terminal
	}
	current := m.terminalViews()
	views := current.terminals
	if len(views) == 0 && !m.watch.linked {
		views = []harness.TerminalID{m.watch.terminalID}
	}
	var shown []harness.TerminalID
	for _, terminal := range views {
		if m.registry.host.ViewOwner(terminal) == current.owner {
			shown = append(shown, terminal)
		}
	}
	switch len(shown) {
	case 0:
		return ""
	case 1:
		return shown[0]
	}
	live := m.registry.host.LiveTerminals(context.Background())
	for _, terminal := range shown {
		if _, ok := live[terminal]; ok {
			return terminal
		}
	}
	return shown[0]
}
