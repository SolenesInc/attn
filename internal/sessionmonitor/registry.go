package sessionmonitor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

type Life interface {
	Go(name string, fn func()) bool
	Do(name string, fn func()) bool
	AfterFunc(name string, delay time.Duration, fn func()) *time.Timer
	Context() context.Context
	Done() <-chan struct{}
	Ended() bool
}

type Host interface {
	Life
	Logf(format string, args ...any)
	Debug() bool
	HomeFor(*Monitor) Home
	Classify(ctx context.Context, request ClassifyRequest) (string, error)
	ReadBranch(ctx context.Context, directory string) (*attngit.BranchInfo, error)

	LiveTerminals(ctx context.Context) map[harness.TerminalID]struct{}
	ViewOwner(harness.TerminalID) string
	Input(ctx context.Context, terminal harness.TerminalID, data []byte) error
	Link(*Monitor, harness.Voice) harness.Link
	LinkTranscriptPath(link, path string) string
	NoteKey(terminal harness.TerminalID, at time.Time)
	LastKey(terminal harness.TerminalID) time.Time
	EnqueueTitle(m *Monitor, conversation, source string) error
}

type ClassifyRequest struct {
	Agent     protocol.SessionAgent
	Directory string
	Text      string
	Timeout   time.Duration
}

type TerminalWatch struct {
	mu         sync.Mutex
	terminalID harness.TerminalID
	linked     bool
	terminal   sessionstate.TerminalEvidence
	monitors   map[*Monitor]struct{}
}

type Monitor struct {
	mu                  sync.Mutex
	session             protocol.SessionID
	stopped             bool
	saveMu              sync.Mutex
	saved               savedView
	registry            *Registry
	home                Home
	label               string
	initialized         bool
	policy              Policy
	agent               protocol.SessionAgent
	startedAt           time.Time
	epoch               string
	revision            uint64
	state               StateView
	driver              DriverMetadata
	turn                TurnStamps
	lastModelRequestAt  time.Time
	firstReportAt       time.Time
	wakeTimer           *time.Timer
	watch               *TerminalWatch
	conversationInput   Conversation
	succeeds            Key
	conversation        string
	localConversation   string
	directory           string
	branch              attngit.BranchInfo
	pullRequests        map[string]PullRequest
	sentFiles           map[string]uint64
	harnessTitle        string
	terminalTitle       string
	exit                *Exit
	costInitialized     bool
	cost                map[string]*sessioncost.Source
	costOwners          map[string]costOwner
	evidence            sessionstate.ConversationEvidence
	resolveAt           time.Time
	resolvePending      bool
	dwell               dwellPending
	reason              string
	trace               *statetrace.Ring
	classifiedTurn      string
	classifyingTurn     string
	forcedStop          time.Time
	silence             *time.Timer
	input               *lane
	autoSettle          *autoSettleTimer
	dismissal           autoSettleDismissal
	activityAt          time.Time
	transcriptWatch     *transcriptWatcher
	pluginUsageWatch    *pluginUsageWatcher
	finalUsage          []func()
	usageRuns           int
	usageIdle           chan struct{}
	usageDraining       bool
	pendingConversation *ConversationObservation
	pendingSwitch       Conversation
	titleAttempted      bool
	titleInitialPrompt  *[sha256.Size]byte
	driverLaunch        *driverLaunch
	touchedAt           time.Time
	views               atomic.Pointer[terminalViews]
	link                string
	windowRevision      uint64
}

type Evidence struct {
	sessionstate.TerminalEvidence
	sessionstate.ConversationEvidence
}

func (e *Evidence) ClearProgramStatus() {
	sessionstate.ClearProgramStatus(&e.TerminalEvidence, &e.ConversationEvidence)
}

type Registry struct {
	host      Host
	saver     Saver
	mu        sync.Mutex
	watches   map[harness.TerminalID]*TerminalWatch
	monitors  map[*Monitor]struct{}
	wake      chan struct{}
	closed    atomic.Bool
	nextLocal uint64
}

func NewRegistry(host Host, saver Saver) *Registry {
	return &Registry{host: host, saver: saver, watches: make(map[harness.TerminalID]*TerminalWatch), monitors: make(map[*Monitor]struct{}), wake: make(chan struct{}, 1)}
}

type Start struct {
	Session      protocol.SessionID
	Terminal     harness.TerminalID
	Conversation string
	Linked       bool
	Candidate    *Monitor
	Launch       func() Launch
	Policy       Policy
}

func (r *Registry) Reuse(previous *Monitor, start Start) bool {
	if previous == nil || previous.watch.terminalID != start.Terminal {
		return false
	}
	previous.mu.Lock()
	defer previous.mu.Unlock()
	if (start.Conversation == "" && !start.Linked) || previous.conversation == start.Conversation {
		previous.initializeLocked(start)
		return true
	}
	if previous.conversation == "" && start.Conversation != "" {
		previous.conversation = start.Conversation
		previous.initializeLocked(start)
		return true
	}
	return false
}

func (r *Registry) Start(previous *Monitor, start Start) *Monitor {
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous != nil {
		r.forgetLocked(previous)
	}
	var w *TerminalWatch
	if !start.Linked {
		w = r.watches[start.Terminal]
	}
	if w == nil && start.Candidate != nil {
		w = start.Candidate.watch
	}
	if w == nil {
		w = &TerminalWatch{terminalID: start.Terminal, linked: start.Linked, monitors: make(map[*Monitor]struct{})}
	}
	if !start.Linked {
		r.watches[start.Terminal] = w
	}
	r.nextLocal++
	m := start.Candidate
	if m == nil {
		m = &Monitor{registry: r, epoch: rand.Text()}
		m.home = r.host.HomeFor(m)
	}
	m.succeeds = Key{}
	m.watch, m.conversation, m.localConversation = w, start.Conversation, "local:"+strconv.FormatUint(r.nextLocal, 10)
	m.session = start.Session
	m.initializeLocked(start)
	m.input = m.freshLaneLocked()
	m.saved.view = m.snapshotLocked()
	w.monitors[m] = struct{}{}
	r.monitors[m] = struct{}{}
	return m
}

func (m *Monitor) Arm() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.armWakeLocked()
	m.mu.Unlock()
}

func (m *Monitor) Home() Home { return m.home }

func (m *Monitor) Snapshot() View {
	if m == nil {
		return View{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Monitor) TakeUserSubmit(previous *Monitor) {
	if m == nil {
		return
	}
	previous.mu.Lock()
	typed := previous.input.userSubmit
	previous.input.userSubmit = false
	previous.mu.Unlock()
	if typed {
		m.mu.Lock()
		m.input.userSubmit = true
		m.mu.Unlock()
	}
}

func (m *Monitor) initializeLocked(start Start) {
	m.policy = start.Policy
	if m.initialized {
		return
	}
	launch := start.Launch()
	m.agent = launch.Agent
	m.link = launch.Link
	m.startedAt = launch.StartedAt
	m.directory = launch.Directory
	m.branch = launch.Branch
	m.harnessTitle = launch.HarnessTitle
	m.state = StateView{State: launch.State, Since: launch.StateSince, Updated: launch.StateUpdated}
	m.lastModelRequestAt = launch.LastModelRequestAt
	m.turn = launch.Turn
	m.driver = launch.Driver
	m.loadCostLocked(launch.Cost)
	m.exit = launch.Exit
	m.conversationInput = Conversation{ConversationObservation: launch.Conversation}
	m.touchedAt = launch.LastSeen
	m.pendingSwitch = launch.PendingSwitch
	m.pullRequests = launch.PullRequests
	m.initialized = launch.Known
}

func (m *Monitor) Forget() {
	if m == nil {
		return
	}
	r := m.registry
	m.Stop()
	r.mu.Lock()
	r.forgetLocked(m)
	r.mu.Unlock()
	r.rearm()
}

func (r *Registry) forgetLocked(m *Monitor) {
	w := m.watch
	delete(r.monitors, m)
	if w == nil {
		return
	}
	delete(w.monitors, m)
	if len(w.monitors) == 0 && r.watches[w.terminalID] == w {
		delete(r.watches, w.terminalID)
	}
}

func (r *Registry) each() []*Monitor {
	r.mu.Lock()
	defer r.mu.Unlock()
	monitors := make([]*Monitor, 0, len(r.monitors))
	for m := range r.monitors {
		monitors = append(monitors, m)
	}
	return monitors
}

func (m *Monitor) Stop() {
	if m == nil {
		return
	}
	m.FinishUsage()
	m.saveMu.Lock()
	m.mu.Lock()
	lane := m.input
	lane.stopRetriesLocked()
	if m.autoSettle != nil {
		m.autoSettle.timer.Stop()
		m.autoSettle = nil
	}
	if m.silence != nil {
		m.silence.Stop()
		m.silence = nil
	}
	if m.wakeTimer != nil {
		m.wakeTimer.Stop()
		m.wakeTimer = nil
	}
	m.resolvePending = false
	m.stopped = true
	m.stopSaveRetryLocked()
	m.mu.Unlock()
	m.saveMu.Unlock()
	lane.running.Wait()
}

func (r *Registry) rearm() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Registry) Close() {
	r.closed.Store(true)
	monitors := r.each()
	lanes := make([]*lane, 0, len(monitors))
	for _, m := range monitors {
		m.mu.Lock()
		lanes = append(lanes, m.input)
		if m.silence != nil {
			m.silence.Stop()
			m.silence = nil
		}
		m.mu.Unlock()
	}
	for _, lane := range lanes {
		lane.mu.Lock()
		lane.stopRetriesLocked()
		lane.mu.Unlock()
	}
	for _, lane := range lanes {
		lane.running.Wait()
	}
}

type driverLaunch struct {
	plugin  string
	run     string
	replays []func()
	exit    *ptybackend.ExitInfo
}
