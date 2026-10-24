package sessionmonitor

import (
	"context"
	"crypto/sha256"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

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
	HomeFor(*Entry) Home
	Classify(ctx context.Context, request ClassifyRequest) (string, error)
	ReadBranch(ctx context.Context, directory string) (*attngit.BranchInfo, error)

	LiveTerminals(ctx context.Context) map[harness.TerminalID]struct{}
	ViewOwner(harness.TerminalID) string
	Input(ctx context.Context, terminal harness.TerminalID, data []byte) error
	Link(*Entry, harness.Voice) harness.Link
	LinkTranscriptPath(link, path string) string
	NoteKey(terminal harness.TerminalID, at time.Time)
	LastKey(terminal harness.TerminalID) time.Time
	EnqueueTitle(e *Entry, conversation, source string) error
}

type ClassifyRequest struct {
	Agent     protocol.SessionAgent
	Directory string
	Text      string
	Timeout   time.Duration
}

type Monitor struct {
	mu         sync.Mutex
	terminalID harness.TerminalID
	runtime    RuntimeID
	terminal   sessionstate.TerminalEvidence
	entries    map[*Entry]struct{}
}

type Entry struct {
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
	revision            uint64
	state               StateView
	driver              DriverMetadata
	turn                TurnStamps
	lastModelRequestAt  time.Time
	firstReportAt       time.Time
	wakeTimer           *time.Timer
	monitor             *Monitor
	conversationInput   Conversation
	succeeds            Key
	conversation        string
	localConversation   string
	directory           string
	branch              attngit.BranchInfo
	pullRequests        map[string]PullRequest
	sentFiles           map[string]uint64
	harnessTitle        string
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
	titleAttempted      bool
	titleInitialPrompt  *[sha256.Size]byte
	driverLaunch        *driverLaunch
	touchedAt           time.Time
	views               atomic.Pointer[terminalViews]
	link                string
	claimedNatives      map[string]bool
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
	physical  map[harness.TerminalID]*Monitor
	entries   map[*Entry]struct{}
	wake      chan struct{}
	closed    atomic.Bool
	nextLocal uint64
}

func NewRegistry(host Host, saver Saver) *Registry {
	return &Registry{host: host, saver: saver, physical: make(map[harness.TerminalID]*Monitor), entries: make(map[*Entry]struct{}), wake: make(chan struct{}, 1)}
}

type Start struct {
	Session      protocol.SessionID
	Terminal     harness.TerminalID
	Conversation string
	Linked       bool
	Candidate    *Entry
	Launch       func() Launch
	Policy       Policy
}

func (r *Registry) Reuse(previous *Entry, start Start) bool {
	if previous == nil || previous.monitor.terminalID != start.Terminal {
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

func (r *Registry) Start(previous *Entry, start Start) *Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous != nil {
		r.forgetLocked(previous)
	}
	var m *Monitor
	if !start.Linked {
		m = r.physical[start.Terminal]
	}
	if m == nil && start.Candidate != nil {
		m = start.Candidate.monitor
	}
	if m == nil {
		m = &Monitor{terminalID: start.Terminal, entries: make(map[*Entry]struct{})}
		if start.Linked {
			m.runtime = RuntimeID(uuid.NewString())
		}
	}
	if !start.Linked {
		r.physical[start.Terminal] = m
	}
	r.nextLocal++
	e := start.Candidate
	if e == nil {
		e = &Entry{registry: r}
		e.home = r.host.HomeFor(e)
	}
	e.succeeds = Key{}
	e.monitor, e.conversation, e.localConversation = m, start.Conversation, "local:"+strconv.FormatUint(r.nextLocal, 10)
	e.input = &lane{mu: &e.mu, entry: e, stopped: r.closed.Load()}
	e.session = start.Session
	e.initializeLocked(start)
	e.saved.view = e.snapshotLocked()
	m.entries[e] = struct{}{}
	r.entries[e] = struct{}{}
	return e
}

func (e *Entry) Arm() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.armWakeLocked()
	e.mu.Unlock()
}

func (e *Entry) Home() Home { return e.home }

func (e *Entry) Snapshot() View {
	if e == nil {
		return View{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

func (e *Entry) TakeUserSubmit(previous *Entry) {
	if e == nil {
		return
	}
	previous.mu.Lock()
	typed := previous.input.userSubmit
	previous.input.userSubmit = false
	previous.mu.Unlock()
	if typed {
		e.mu.Lock()
		e.input.userSubmit = true
		e.mu.Unlock()
	}
}

func (e *Entry) initializeLocked(start Start) {
	e.policy = start.Policy
	if e.initialized {
		return
	}
	launch := start.Launch()
	e.agent = launch.Agent
	e.link = launch.Link
	e.startedAt = launch.StartedAt
	e.directory = launch.Directory
	e.branch = launch.Branch
	e.harnessTitle = launch.HarnessTitle
	e.state = StateView{State: launch.State, Since: launch.StateSince, Updated: launch.StateUpdated}
	e.lastModelRequestAt = launch.LastModelRequestAt
	e.turn = launch.Turn
	e.driver = launch.Driver
	e.loadCostLocked(launch.Cost)
	e.exit = launch.Exit
	e.conversationInput = Conversation{ConversationObservation: launch.Conversation}
	e.touchedAt = launch.LastSeen
	e.initialized = launch.Known
}

func (e *Entry) Forget() {
	if e == nil {
		return
	}
	r := e.registry
	e.Stop()
	r.mu.Lock()
	r.forgetLocked(e)
	r.mu.Unlock()
	r.rearm()
}

func (r *Registry) forgetLocked(e *Entry) {
	m := e.monitor
	delete(r.entries, e)
	if m == nil {
		return
	}
	delete(m.entries, e)
	if len(m.entries) == 0 && r.physical[m.terminalID] == m {
		delete(r.physical, m.terminalID)
	}
}

func (r *Registry) each() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]*Entry, 0, len(r.entries))
	for e := range r.entries {
		entries = append(entries, e)
	}
	return entries
}

func (e *Entry) Stop() {
	if e == nil {
		return
	}
	e.FinishUsage()
	e.saveMu.Lock()
	e.mu.Lock()
	lane := e.input
	lane.stopRetriesLocked()
	if e.autoSettle != nil {
		e.autoSettle.timer.Stop()
		e.autoSettle = nil
	}
	if e.silence != nil {
		e.silence.Stop()
		e.silence = nil
	}
	if e.wakeTimer != nil {
		e.wakeTimer.Stop()
		e.wakeTimer = nil
	}
	e.resolvePending = false
	e.stopped = true
	e.stopSaveRetryLocked()
	e.mu.Unlock()
	e.saveMu.Unlock()
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
	entries := r.each()
	lanes := make([]*lane, 0, len(entries))
	for _, e := range entries {
		e.mu.Lock()
		lanes = append(lanes, e.input)
		if e.silence != nil {
			e.silence.Stop()
			e.silence = nil
		}
		e.mu.Unlock()
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
