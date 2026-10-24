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
	Applied(View) bool
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
}

func (e *Entry) SetPolicy(policy Policy) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.policy = policy
	e.mu.Unlock()
}

func (e *Entry) Policy() Policy {
	if e == nil {
		return Policy{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.policy
}

func (e *Entry) Bind(label string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.label = label
	e.mu.Unlock()
}

func (e *Entry) Label() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.label
}

func (e *Entry) logf(format string, args ...any) { e.registry.host.Logf(format, args...) }

func (e *Entry) current() bool {
	e.registry.mu.Lock()
	defer e.registry.mu.Unlock()
	_, ok := e.registry.entries[e]
	return ok
}

func (e *Entry) Launched(at time.Time) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.startedAt = at
	e.mu.Unlock()
}

func (e *Entry) Touch(at time.Time) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	e.touchedAt = at
	view := e.viewLocked()
	e.mu.Unlock()
	return e.publish(view)
}

type terminalViews struct {
	owner     string
	terminals []harness.TerminalID
}

func (e *Entry) SetTerminals(owner string, terminals []harness.TerminalID) {
	if e == nil {
		return
	}
	e.views.Store(&terminalViews{owner: owner, terminals: terminals})
}

func (e *Entry) terminalViews() terminalViews {
	if views := e.views.Load(); views != nil {
		return *views
	}
	return terminalViews{}
}

func (e *Entry) primaryTerminalLocked() harness.TerminalID {
	current := e.terminalViews()
	views := current.terminals
	if len(views) == 0 && e.monitor.runtime == "" {
		views = []harness.TerminalID{e.monitor.terminalID}
	}
	var shown []harness.TerminalID
	for _, terminal := range views {
		if e.registry.host.ViewOwner(terminal) == current.owner {
			shown = append(shown, terminal)
		}
	}
	switch len(shown) {
	case 0:
		return ""
	case 1:
		return shown[0]
	}
	live := e.registry.host.LiveTerminals(context.Background())
	for _, terminal := range shown {
		if _, ok := live[terminal]; ok {
			return terminal
		}
	}
	return shown[0]
}
