package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/singleflight"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/diag"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/statetrace"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

type workerReconcileReport struct {
	Created           int
	StateUpdated      int
	MarkedIdle        int
	MarkedRecoverable int
	Reaped            int
	SkippedIdle       int
	SkippedRecent     int
	SkippedShell      int
	LikelyAlive       int
	LivenessUnknown   int
	MissingMetadata   int
	Changed           bool
	ChangedSessionIDs []string
}

func (r *workerReconcileReport) markChanged(sessionID protocol.SessionID) {
	r.Changed = true
	r.ChangedSessionIDs = append(r.ChangedSessionIDs, string(sessionID))
}

const (
	forcedStopSuppressTTL = 30 * time.Second
	branchMonitorInterval = 15 * time.Second

	backupInterval = 6 * time.Hour
	backupKeep     = 12

	startupRecoveryRetryMax       = 2
	startupRecoveryRetryDelay     = 500 * time.Millisecond
	deferredRecoveryMaxAttempts   = 3
	deferredRecoveryRetryInterval = 10 * time.Second
	deferredRecoveryRPCTimeout    = 5 * time.Second
	workerStartupProbeTimeout     = 20 * time.Second

	warnWorkerRecoveryPartial     = "worker_recovery_partial"
	warnStaleSessionsPruned       = "stale_sessions_pruned"
	warnStaleSessionMissingWorker = "stale_session_missing_worker"
	warnPTYBackendFallback        = "pty_backend_fallback"
	warnPTYBackendUnsupported     = "pty_backend_unsupported"
	warnGHNotInstalled            = "gh_not_installed"
	warnGHVersionTooOld           = "gh_version_too_old"
)

var ErrAlreadyRunning = errors.New("daemon already running")

type Daemon struct {
	// Nil in production; wire tests pause a profile action here, its broadcasts held, to land a change meanwhile.
	heldActionRan    atomic.Pointer[func()]
	socketPath       string
	pidPath          string
	pidFile          *os.File
	dataRoot         string
	daemonInstanceID string
	clientToken      string
	store            *store.Store
	// Serializes PR fetches with review-request edge reconciliation.
	prRefreshMu                       sync.Mutex
	automationMu                      sync.Mutex
	automationLaunchFailures          sync.Map
	automationObservationMu           sync.Mutex
	automationObservationLocks        map[string]*sync.Mutex
	automationRepoMu                  sync.Mutex
	automationRepos                   map[string]*sync.Mutex
	automationDeliveryHook            func(*store.AutomationRun) error
	wsWriteTimeout                    time.Duration
	wsPingInterval                    time.Duration
	wsPingTimeout                     time.Duration
	listener                          net.Listener
	httpServer                        *http.Server
	httpListener                      net.Listener
	httpHandler                       http.Handler
	diagServer                        *diag.Server
	harnessWSListenerFD               string
	wsHub                             *wsHub
	presentSince                      time.Time
	presenceMu                        sync.RWMutex
	crewLifecycleState                *crewLifecycleMemo
	crewMemoOnce                      sync.Once
	crewCharterMu                     sync.Mutex
	life                              lifetime
	stopOnce                          sync.Once
	logger                            *logging.Logger
	debugLogging                      bool
	ghRegistry                        *github.ClientRegistry
	hubManager                        *hub.Manager
	classifier                        Classifier
	repoVisibilityKnown               map[string]string
	repoVisibilityPending             map[string]bool
	repoVisibilityMu                  sync.Mutex
	reopenGitMu                       sync.Mutex
	reopenBranches                    *sharedCalls[reopenBranchKey, branchInspection]
	reopenInspect                     func(context.Context, *git.Client, string, string) (branchInspection, error)
	gitReaderMu                       sync.Mutex
	gitStatus                         *gitStatusReader
	fileDiff                          *fileDiffReader
	gitExec                           gitExecutor
	worktreeMaintenance               worktreeMaintenanceCoordinator
	warnings                          []protocol.DaemonWarning
	warningsMu                        sync.RWMutex
	ptyBackend                        ptybackend.Backend
	ptySettingsMu                     sync.Mutex
	ptySettingsChangeMu               sync.Mutex
	sharedPTYHost                     *ptybackend.WorkerBackend
	upgradingMu                       sync.Mutex
	upgradingWorkers                  map[harness.TerminalID]bool
	watchersMu                        sync.Mutex
	transcriptWatch                   map[protocol.SessionID]*transcriptWatcher
	pluginUsageWatch                  map[protocol.SessionID]*pluginUsageWatcher
	finalUsage                        map[protocol.SessionID][]func()
	usageRuns                         map[protocol.SessionID]int
	usageIdle                         map[protocol.SessionID]chan struct{}
	usageDraining                     map[protocol.SessionID]bool
	transcriptWatcherSessionLookup    func(string) *protocol.Session
	transcriptResumeLookup            func(protocol.SessionAgent, string) string
	classifiedMu                      sync.Mutex
	classifiedTurn                    map[protocol.SessionID]string
	classifyingTurn                   map[protocol.SessionID]string
	classificationTranscriptExtractor func(*protocol.Session, string, int, time.Time) (string, string, error)
	forcedStopMu                      sync.Mutex
	forcedStop                        map[protocol.SessionID]time.Time
	pendingConversationMu             sync.Mutex
	pendingConversation               map[protocol.SessionID]agentConversationObservation
	sessionTitleMu                    sync.Mutex
	sessionTitleExec                  func(ctx context.Context, session *protocol.Session, conversation string) (string, error)
	sessionTitleAttempted             map[protocol.SessionID]struct{}
	sessionTitleInitialPrompt         map[protocol.SessionID][sha256.Size]byte
	seedArtifactMu                    sync.Mutex
	delegationModelQueries            singleflight.Group
	delegationMu                      sync.Mutex
	delegationRunning                 map[string]bool
	delegationCheckoutMu              sync.Mutex
	delegationWaitsForFirstTurn       bool
	launchWatchMu                     sync.Mutex
	launchWatches                     map[protocol.SessionID]*launchWatch
	recoveredLaunches                 map[protocol.SessionID]*launchWatch
	terminalExitIntentMu              sync.Mutex
	terminalExitIntents               map[harness.TerminalID]terminalExitIntent
	prepareSessionTeardownHook        func(string) error
	teardownMu                        sync.Mutex
	tearingDown                       map[protocol.SessionID]chan struct{}
	sessionLifecycleLocks             sessionLocks
	terminalEndLocks                  sessionLocks
	spawnLocksMu                      sync.Mutex
	spawnLocks                        map[protocol.SessionID]*spawnLock
	sessionInputOnce                  sync.Once
	sessionInputState                 *sessionInputModule
	terminalsOnce                     sync.Once
	terminalState                     *terminalRegistry
	inboxMu                           sync.Mutex
	inboxStates                       map[inbox.Address]*inboxDeliveryState
	inboxUnsubscribe                  func()
	crewWakeMu                        sync.Mutex
	crewExitedMu                      sync.Mutex
	crewExitedSessions                map[string]string
	stateTraceOnce                    sync.Once
	stateTrace                        *statetrace.Recorder
	sessionEvidenceOnce               sync.Once
	sessionEvidence                   *sessionEvidenceTable
	sessionDwellOnce                  sync.Once
	sessionDwell                      *dwellGate
	sessionResolverOnce               sync.Once
	sessionResolverState              *sessionResolver
	pluginDriverSilenceOnce           sync.Once
	pluginDriverSilenceWatch          *pluginDriverSilenceWatch
	pluginDriverSilenceGraceOverride  time.Duration
	sessionStateReasonOnce            sync.Once
	sessionStateReason                *sessionStateReasons
	supportInputTraceOnce             sync.Once
	supportInputTrace                 *supportInputTraceRing
	lastInputMu                       sync.Mutex
	lastAutoSettleActivityAt          map[protocol.SessionID]time.Time
	autoSettleFireMu                  sync.Mutex

	emptyDesktops emptyDesktopRemoval

	autoSettleMu         sync.Mutex
	autoSettleTimers     map[protocol.SessionID]*autoSettleTimer
	autoSettleDismissals map[protocol.SessionID]bool

	snoozeMu sync.Mutex

	recoveryMu          sync.RWMutex
	recovering          bool
	recoverySettled     chan struct{}
	notebookMu          sync.Mutex
	notebookStore       *notebook.Store
	notebookWatcherMu   sync.Mutex
	notebookWatcher     *notebook.Watcher
	notebookWatchedRoot string
	fsMu                sync.Mutex
	fsStores            map[string]*fsdoc.Store
	fsWatchMu           sync.Mutex
	fsWatchers          map[string]*fsRootWatch
	pendingInitialWS    map[*wsClient]struct{}
	startedOnce         sync.Once
	startedCh           chan struct{}
	tailscale           *tailscaleRuntime
	plugins             *pluginRegistry
	pluginSupervisorMu  sync.Mutex
	pluginSupervisor    *pluginSupervisor
	pluginHealthEnabled bool
	pluginDriverMu      sync.Mutex
	pluginLaunching     map[protocol.SessionID]pluginSessionLaunch
	pluginReports       map[protocol.SessionID][]pendingPluginReport
	pluginExits         map[protocol.SessionID]ptybackend.ExitInfo
	pluginDir           string
	bundledPluginDir    string
	busPinMu            sync.Mutex
	busPinEpisodes      map[string]*busPinEpisode
	busPinAge           time.Duration
	removePlugin        func(pluginDir, name string) error
	pluginActionMu      sync.Mutex
	bundledPluginMu     sync.Mutex
	bundledPluginSet    map[string]struct{}
	bundledPluginLoaded bool

	worktreePluginCallTimeout         time.Duration
	worktreeCreateProviderCallTimeout time.Duration

	loginShellEnvMu sync.RWMutex
	loginShellEnv   []string

	terminalThemeMu sync.Mutex
	terminalTheme   pty.TerminalTheme

	currentAgentMu        sync.RWMutex
	currentAgentSessionID protocol.SessionID

	openTileMu sync.Mutex

	desktopTiles desktopTileDelivery

	browserControlMu sync.Mutex
	browserControl   map[string]browserControlPending

	lastBackupMu sync.Mutex
	lastBackupAt time.Time

	workflowBroadcastMu    sync.Mutex
	workflowDirty          map[string]bool
	workflowEngineMu       sync.Mutex
	workflowEngineConn     map[string]workflowEngineSink
	gardenMintNoteID       func() (string, error)
	gardenNow              func() time.Time
	gitHubPollingOffLogged bool
	gardenWatchMu          sync.Mutex
	// gardenBellsResolvedThrough is the last bus seq whose seed bells were resolved; gardenWatchMu guards it.
	gardenBellsResolvedThrough int64
	gardenReviewMu             sync.Mutex
	dispatchSeedsMu            sync.Mutex
	dispatchSeeds              map[protocol.SessionID]string
	dispatchersBySession       map[protocol.SessionID]garden.Tender
	dispatchFromChief          map[protocol.SessionID]bool
	dispatchProjectionRevs     map[protocol.SessionID]int64
	dispatchSeedsLoaded        bool

	automationsBroadcastHook func(*protocol.AutomationsChangedMessage)

	eventBus                       *bus.Bus
	busUnsubscribe                 func()
	gardenSeedEventConsumerErr     error
	gardenSeedEventConsumerStarted bool

	docSubsMu              sync.Mutex
	docSubs                map[string]*docSubscription
	docSubsSeq             int64
	docUnsubHooks          func()
	conversationUnsubHooks func()
	sessionPRUnsubHooks    func()
	sessionPRHosts         func(host string) (sessionPRHost, bool)
	sessionPRRefreshMu     sync.Mutex
	beforeSeedMoveWrite    func(seedID string)

	harvestWhenMu        sync.Mutex
	harvestWhenUntracked map[string]bool

	notebookPendingMu    sync.Mutex
	notebookPendingPaths map[string][]string

	snapshotMu           sync.Mutex
	snapshotDepth        int
	pendingSnapshots     map[string]func()
	pendingSnapshotOrder []string

	conversationKeepMu   sync.Mutex
	jobQueueMu           sync.RWMutex
	jobQueue             *jobs.Runner
	taskFailureRenderers map[string]taskFailureRenderer

	sessionActivityRunsMu sync.Mutex
	sessionActivityRuns   map[protocol.SessionID]sessionActivityRun
}

func (d *Daemon) addWarning(code, message string) {
	d.warningsMu.Lock()
	defer d.warningsMu.Unlock()
	for _, w := range d.warnings {
		if w.Code == code && w.Message == message {
			return
		}
	}
	d.warnings = append(d.warnings, protocol.DaemonWarning{
		Code:    code,
		Message: message,
	})
}

func (d *Daemon) getWarnings() []protocol.DaemonWarning {
	d.warningsMu.RLock()
	defer d.warningsMu.RUnlock()
	if len(d.warnings) == 0 {
		return nil
	}
	result := make([]protocol.DaemonWarning, len(d.warnings))
	copy(result, d.warnings)
	return result
}

func (d *Daemon) clearWarnings() {
	d.warningsMu.Lock()
	defer d.warningsMu.Unlock()
	d.warnings = nil
}

func (d *Daemon) setRecovering(value bool) {
	var pending []*wsClient

	d.recoveryMu.Lock()
	if value && !d.recovering {
		d.recoverySettled = make(chan struct{})
	}
	if !value && d.recovering && d.recoverySettled != nil {
		close(d.recoverySettled)
	}
	d.recovering = value
	if !value {
		pending = make([]*wsClient, 0, len(d.pendingInitialWS))
		for client := range d.pendingInitialWS {
			pending = append(pending, client)
		}
		d.pendingInitialWS = make(map[*wsClient]struct{})
	}
	d.recoveryMu.Unlock()

	if !value {
		for _, client := range pending {
			d.sendInitialState(client)
		}
	}
}

func (d *Daemon) isRecovering() bool {
	d.recoveryMu.RLock()
	defer d.recoveryMu.RUnlock()
	return d.recovering
}

var recoveryAlreadySettled = func() <-chan struct{} {
	settled := make(chan struct{})
	close(settled)
	return settled
}()

func (d *Daemon) recoverySettledSignal() <-chan struct{} {
	d.recoveryMu.RLock()
	defer d.recoveryMu.RUnlock()
	if !d.recovering || d.recoverySettled == nil {
		return recoveryAlreadySettled
	}
	return d.recoverySettled
}

func (d *Daemon) scheduleInitialState(client *wsClient) {
	sendNow := false

	d.recoveryMu.Lock()
	if d.recovering {
		d.pendingInitialWS[client] = struct{}{}
	} else {
		sendNow = true
	}
	d.recoveryMu.Unlock()

	if sendNow {
		d.sendInitialState(client)
	}
}

func (d *Daemon) dropPendingInitialState(client *wsClient) {
	d.recoveryMu.Lock()
	defer d.recoveryMu.Unlock()
	delete(d.pendingInitialWS, client)
}

func (d *Daemon) warmLoginShellEnvCache() {
	shell := pty.GetUserLoginShell()
	if shell == "" {
		return
	}
	env, err := pty.ReadLoginShellEnv(shell)
	if err != nil {
		d.logf("login shell env pre-warm failed for %s: %v", shell, err)
		return
	}
	d.loginShellEnvMu.Lock()
	d.loginShellEnv = env
	d.loginShellEnvMu.Unlock()
	d.logf("login shell env pre-warmed: shell=%s vars=%d", shell, len(env))
}

func (d *Daemon) cachedLoginShellEnv() []string {
	d.loginShellEnvMu.RLock()
	env := d.loginShellEnv
	d.loginShellEnvMu.RUnlock()
	return env
}

func (d *Daemon) currentTerminalTheme() pty.TerminalTheme {
	d.terminalThemeMu.Lock()
	theme := d.terminalTheme
	d.terminalThemeMu.Unlock()
	return theme
}

func (d *Daemon) setCurrentTerminalTheme(theme pty.TerminalTheme) {
	d.terminalThemeMu.Lock()
	d.terminalTheme = theme
	d.terminalThemeMu.Unlock()
}

func (d *Daemon) RecoverGUIPath() {
	if err := pathutil.EnsureGUIPath(); err != nil {
		d.logf("PATH recovery failed: %v", err)
	}
}

func (d *Daemon) RemoveLegacyStateFile() {
	legacyPath := config.StatePath()
	if os.Remove(legacyPath) == nil {
		d.logf("Removed legacy state file: %s", legacyPath)
	}
}

func (d *Daemon) ScrubInheritedAgentSessionEnv() {
	if scrubbed := config.ScrubInheritedAgentSessionEnv(); len(scrubbed) > 0 {
		d.logf("scrubbed inherited agent session env before startup: %v", scrubbed)
	}
}

func (d *Daemon) signalStarted() {
	d.startedOnce.Do(func() {
		if d.startedCh == nil {
			d.startedCh = make(chan struct{})
		}
		close(d.startedCh)
	})
}

func (d *Daemon) Started() <-chan struct{} {
	return d.startedCh
}

func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())

	classifier.SetLogger(func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	})
	git.SetLogFunc(func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	})

	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	manager := pty.NewManager(logger.Infof)

	d := &Daemon{
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		wsHub:               newWSHub(),
		presentSince:        time.Now(),
		desktopTiles:        newDesktopTileDelivery(),
		logger:              logger,
		debugLogging:        logger != nil && logger.DebugEnabled(),
		ghRegistry:          github.NewClientRegistry(),
		hubManager:          nil,
		workflowDirty:       make(map[string]bool),
		workflowEngineConn:  make(map[string]workflowEngineSink),
		ptyBackend:          ptybackend.NewEmbedded(manager),
		transcriptWatch:     make(map[protocol.SessionID]*transcriptWatcher),
		pendingInitialWS:    make(map[*wsClient]struct{}),
		startedCh:           make(chan struct{}),
		classifiedTurn:      make(map[protocol.SessionID]string),
		classifyingTurn:     make(map[protocol.SessionID]string),
		forcedStop:          make(map[protocol.SessionID]time.Time),
		pendingConversation: make(map[protocol.SessionID]agentConversationObservation),
		tailscale:           newTailscaleRuntime(),
		plugins:             newPluginRegistry(),
		pluginHealthEnabled: true,
		pluginDir:           pluginDirForSocket(socketPath),
		bundledPluginDir:    bundledPluginDirForExecutable(),
		spawnLocks:          make(map[protocol.SessionID]*spawnLock),
	}
	d.wireGitExecution(productionGitExecutorConfig)
	d.delegationWaitsForFirstTurn = true
	d.sessionTitleExec = d.execSessionTitle
	return d
}

func NewForTesting(socketPath string) *Daemon {
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	manager := pty.NewManager(nil)
	d := &Daemon{
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               store.New(),
		wsHub:               newWSHub(),
		presentSince:        time.Now(),
		desktopTiles:        newDesktopTileDelivery(),
		logger:              nil,
		ghRegistry:          github.NewClientRegistry(),
		hubManager:          nil,
		ptyBackend:          ptybackend.NewEmbedded(manager),
		transcriptWatch:     make(map[protocol.SessionID]*transcriptWatcher),
		pendingInitialWS:    make(map[*wsClient]struct{}),
		startedCh:           make(chan struct{}),
		classifiedTurn:      make(map[protocol.SessionID]string),
		classifyingTurn:     make(map[protocol.SessionID]string),
		forcedStop:          make(map[protocol.SessionID]time.Time),
		pendingConversation: make(map[protocol.SessionID]agentConversationObservation),
		tailscale:           newTailscaleRuntime(),
		plugins:             newPluginRegistry(),
		pluginDir:           pluginDirForSocket(socketPath),
		bundledPluginDir:    bundledPluginDirForExecutable(),
		workflowDirty:       make(map[string]bool),
		workflowEngineConn:  make(map[string]workflowEngineSink),
		spawnLocks:          make(map[protocol.SessionID]*spawnLock),
		jobQueue:            jobs.New(jobs.Options{}),
	}
	d.wireGitExecution(productionGitExecutorConfig)
	d.ensureEventBus()
	return d
}

func (d *Daemon) removeLegacyStateFile() {
	legacyPath := config.StatePath()
	if _, err := os.Stat(legacyPath); err == nil {
		os.Remove(legacyPath)
		d.logf("Removed legacy state file: %s", legacyPath)
	}
}

func (d *Daemon) Start() error {
	d.harnessWSListenerFD = os.Getenv("ATTN_HARNESS_WS_LISTENER_FD")
	_ = os.Unsetenv("ATTN_HARNESS_WS_LISTENER_FD")
	if d.dataRoot == "" {
		d.dataRoot = filepath.Dir(d.socketPath)
	}
	if d.pendingInitialWS == nil {
		d.pendingInitialWS = make(map[*wsClient]struct{})
	}
	if d.startedCh == nil {
		d.startedCh = make(chan struct{})
	}
	if d.transcriptWatch == nil {
		d.transcriptWatch = make(map[protocol.SessionID]*transcriptWatcher)
	}
	if d.classifiedTurn == nil {
		d.classifiedTurn = make(map[protocol.SessionID]string)
	}
	if d.classifyingTurn == nil {
		d.classifyingTurn = make(map[protocol.SessionID]string)
	}
	if d.forcedStop == nil {
		d.forcedStop = make(map[protocol.SessionID]time.Time)
	}
	if d.ptyBackend == nil {
		d.ptyBackend = ptybackend.NewEmbedded(pty.NewManager(d.logf))
	}
	if d.tailscale == nil {
		d.tailscale = newTailscaleRuntime()
	}
	if d.plugins == nil {
		d.plugins = newPluginRegistry()
	}
	startSucceeded := false
	if err := enrollment.RefuseOutpost(d.dataRoot); err != nil {
		return err
	}
	if err := d.acquirePIDLock(); err != nil {
		return fmt.Errorf("acquire PID lock: %w", err)
	}
	defer func() {
		if startSucceeded {
			return
		}
		d.Stop()
	}()
	if d.daemonInstanceID == "" {
		instanceID, err := enrollment.EnsureDaemonID(d.dataRoot)
		if err != nil {
			return fmt.Errorf("ensure daemon instance id: %w", err)
		}
		d.daemonInstanceID = instanceID
	}
	if err := d.openStore(); err != nil {
		return err
	}
	d.emptyDesktops.grace = emptyDesktopGraceFromEnv()
	d.store.OnEmptyDesktop(d.desktopEmptied)
	d.removeLegacyStateFile()
	d.ensurePluginSupervisor()
	d.applyHeadlessContextWindowCap()
	d.applyHeadlessTasksMode()
	if err := d.startEventBus(); err != nil {
		return fmt.Errorf("start event bus: %w", err)
	}
	d.loadTerminals()
	if d.clientToken == "" {
		token, err := config.EnsureClientToken(d.dataRoot)
		if err != nil {
			return fmt.Errorf("ensure client token: %w", err)
		}
		d.clientToken = token
	}
	if err := d.ensureEnrollment(); err != nil {
		return fmt.Errorf("ensure enrollment record: %w", err)
	}
	d.ensureGardenCollections()
	d.ensureCrewCollections()
	d.importCrewHomes()
	d.removeEmptyDesktops()
	d.refreshCurrentAgent()
	if d.hubManager == nil {
		d.hubManager = hub.NewManager(
			d.store,
			d.broadcastEndpointStatusChanged,
			d.publishEndpointSessionsChanged,
			d.broadcastRawWSMessage,
			d.logf,
			d.homeDaemonIDForEnrollment,
		)
	}
	selectedBackend := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_PTY_BACKEND")))
	if selectedBackend == "" {
		selectedBackend = "migrating"
	}
	switch selectedBackend {
	case "embedded":
	case "worker":
		workerBackend, err := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
			DataRoot:         d.dataRoot,
			DaemonInstanceID: d.daemonInstanceID,
			BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_WORKER_BINARY")),
			Logf:             d.logf,
			OnTerminalBuild:  d.handleTerminalBuildChanged,
		})
		if err != nil {
			d.logf("failed to initialize worker PTY backend: %v; falling back to embedded", err)
			d.addWarning(
				warnPTYBackendFallback,
				fmt.Sprintf("Failed to initialize worker PTY backend (%v). Falling back to embedded.", err),
			)
		} else {
			if shouldRunWorkerStartupProbe() {
				probeCtx, cancelProbe := context.WithTimeout(context.Background(), workerStartupProbeTimeout)
				probeErr := workerBackend.Probe(probeCtx)
				cancelProbe()
				if probeErr != nil {
					d.logf("worker PTY backend startup probe failed: %v; falling back to embedded", probeErr)
					d.addWarning(
						warnPTYBackendFallback,
						fmt.Sprintf("Worker PTY backend probe failed (%v). Falling back to embedded.", probeErr),
					)
				} else {
					d.ptyBackend = workerBackend
					d.logf("using PTY backend: worker")
				}
			} else {
				d.ptyBackend = workerBackend
				d.logf("using PTY backend: worker (startup probe disabled)")
			}
		}
	case "shared":
		sharedBackend, err := d.newSharedPTYHost()
		if err != nil {
			d.logf("failed to initialize shared PTY host: %v; falling back to embedded", err)
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Failed to initialize shared PTY host (%v). Falling back to embedded.", err))
		} else {
			d.ptyBackend = sharedBackend
			d.sharedPTYHost = sharedBackend
			d.logf("using PTY backend: shared")
		}
	case "migrating":
		legacyBackend, legacyErr := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
			DataRoot:         d.dataRoot,
			DaemonInstanceID: d.daemonInstanceID,
			BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_WORKER_BINARY")),
			Logf:             d.logf,
			OnTerminalBuild:  d.handleTerminalBuildChanged,
		})
		if legacyErr != nil {
			d.logf("failed to initialize legacy PTY workers: %v; falling back to embedded", legacyErr)
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Failed to initialize legacy PTY workers (%v). Falling back to embedded.", legacyErr))
			break
		}
		sharedBackend, sharedErr := d.newSharedPTYHost()
		if sharedErr != nil {
			d.ptyBackend = legacyBackend
			d.logf("shared PTY host initialization failed: %v; new sessions remain on legacy workers", sharedErr)
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Shared PTY host is unavailable (%v). New terminals will keep using dedicated workers.", sharedErr))
			break
		}

		sharedEnabled := parseBooleanSetting(d.store.GetSetting(SettingSharedPTYHostEnabled))
		useSharedForNew := sharedEnabled && sharedBackend.SharedArtifactReady()
		if sharedEnabled && !useSharedForNew && !shouldRunWorkerStartupProbe() && strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")) != "" {
			useSharedForNew = true
		}
		if candidateErr := sharedBackend.SharedCandidateError(); sharedEnabled && !useSharedForNew && candidateErr != nil {
			d.logf("shared PTY host unavailable: %v; new sessions remain on legacy workers", candidateErr)
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Shared PTY host is unavailable (%v). New terminals will keep using dedicated workers.", candidateErr))
		}
		migratingBackend, err := ptybackend.NewMigrating(legacyBackend, sharedBackend, useSharedForNew)
		if err != nil {
			d.logf("failed to initialize PTY migration router: %v; using legacy workers", err)
			d.ptyBackend = legacyBackend
			break
		}
		d.ptyBackend = migratingBackend
		d.sharedPTYHost = sharedBackend
		if useSharedForNew {
			d.logf("using PTY backend: migrating (existing=owner, new=shared)")
		} else {
			d.logf("using PTY backend: migrating (existing=owner, new=legacy)")
		}
	default:
		d.logf("unsupported PTY backend %q, falling back to embedded", selectedBackend)
		d.addWarning(
			warnPTYBackendUnsupported,
			fmt.Sprintf("PTY backend %q is not available in this build. Falling back to embedded.", selectedBackend),
		)
	}

	d.sweepStaleInitialPrompts(time.Now())
	d.life.Go("warmLoginShellEnvCache", d.warmLoginShellEnvCache)

	d.setRecovering(true)
	defer func() {
		if !startSucceeded {
			d.setRecovering(false)
		}
	}()

	previousRunSessions := d.storedSessionIDs()
	if d.listener == nil {
		unixListener, err := listenUnixAtomically(d.socketPath)
		if err != nil {
			return err
		}
		d.listener = unixListener
	}
	listener := d.listener
	d.log("daemon started")
	d.startInstalledPlugins()

	d.wsHub.logf = d.logf

	d.life.Go("startWorkflowBroadcastLoop", func() { d.startWorkflowBroadcastLoop(d.life.Context()) })

	d.life.Go("runMarkdownContentWatcher", func() { d.runMarkdownContentWatcher(d.life.Done()) })

	if hooks, ok := d.ptyBackend.(ptybackend.LifecycleHooks); ok {
		hooks.SetExitHandler(func(info ptybackend.ExitInfo) { d.handlePTYExit(info) })
		hooks.SetStateHandler(d.handlePTYState)
	}

	d.initHTTPServer()
	if err := d.listenHTTP(); err != nil {
		d.logf("%v", err)
		return err
	}
	httpListener := d.httpListener
	d.life.Go("runHTTPServer", func() { d.runHTTPServer(httpListener) })
	d.maybeStartDiagServer()
	d.removeLegacyEmbeddedTailscaleState()
	d.life.Go("ensureTailscaleServeFromSettingsAndBroadcast", d.ensureTailscaleServeFromSettingsAndBroadcast)
	d.hubManager.Start(d.life.Context())

	githubHostsReady := make(chan struct{})
	d.life.Go("refreshGitHubHosts", func() {
		defer close(githubHostsReady)
		if err := d.refreshGitHubHosts(); err != nil {
			d.logf("Initial GitHub host discovery failed: %v", err)
		}
		d.life.Go("pollPRs", d.pollPRs)
		d.life.Go("refreshGitHubHostsLoop", d.refreshGitHubHostsLoop)
	})
	recoveryStartedAt := time.Now()

	d.life.Go("monitorBranches", d.monitorBranches)

	d.life.Go("runModelCaptureLoop", d.runModelCaptureLoop)

	queueStarted := make(chan error, 1)
	if !d.life.Go("jobQueue", func() {
		err := d.startJobQueue()
		queueStarted <- err
		if err != nil {
			return
		}
		runner := d.jobQueueRef()
		<-d.life.Done()
		runner.Stop()
	}) {
		return errDaemonStopping
	}
	if err := <-queueStarted; err != nil {
		return err
	}
	d.startPermanentMaintenance()

	d.watchRecoveredLaunches()
	d.life.Go("startupRecovery", func() {
		pausepoint.At(pausepoint.DaemonStartupRecovery)
		if d.stopping() {
			d.logf("startup recovery skipped: the daemon is stopping; the next daemon recovers")
			return
		}
		d.performStartupPTYRecovery(previousRunSessions, recoveryStartedAt)
		if d.stopping() {
			return
		}
		d.resolveDue(time.Now())
		d.life.Go("runSessionResolver", d.runSessionResolver)
		if _, routed := d.ptyBackend.(*ptybackend.MigratingBackend); routed {
			d.life.Go("validateSharedPTYHostAfterRecovery", d.validateSharedPTYHostAfterRecovery)
		} else {
			d.validateSharedPTYHostAfterRecovery()
		}
		if d.stopping() {
			return
		}
		d.reconcileCrewRestarts()
		if d.stopping() {
			return
		}
		d.lockGardenRoles()
		gardenBellErr := d.discardAllIneligibleGardenSeedBellsLocked()
		d.unlockGardenRoles()
		if gardenBellErr != nil {
			d.logf("Garden seed mailbox startup reconciliation failed; queued updates remain undelivered: %v", gardenBellErr)
		} else {
			d.recoverInbox()
		}
		if !recoverAutomationsAfterGitHubReady(githubHostsReady, d.life.Done(), d.recoverAutomations) {
			return
		}
		d.setRecovering(false)
		d.resumePendingDelegations()
	})

	d.signalStarted()
	d.life.Go("reconcileSeedArtifactObservations", func() {
		if err := d.reconcileSeedArtifactObservations(); err != nil {
			d.logf("Garden seed artifact startup reconciliation incomplete: %v", err)
		}
	})
	startSucceeded = true

	for {
		select {
		case <-d.life.Done():
			return nil
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-d.life.Done():
				return nil
			default:
				d.logf("accept error: %v", err)
				continue
			}
		}

		goTransport(func() { d.handleConnection(conn) })
	}
}

func (d *Daemon) storedSessionIDs() map[protocol.SessionID]struct{} {
	ids := make(map[protocol.SessionID]struct{})
	for _, session := range d.store.List("") {
		ids[session.ID] = struct{}{}
	}
	return ids
}

func (d *Daemon) pruneSessionsWithoutPTY(previousRunSessions map[protocol.SessionID]struct{}, recoveryStartedAt time.Time) int {
	if d.store == nil {
		return 0
	}

	liveIDs := d.liveSessions(context.Background())

	sessions := d.store.List("")
	removed := 0
	recoverable := 0
	for _, session := range sessions {
		if _, fromPreviousRun := previousRunSessions[session.ID]; !fromPreviousRun {
			continue
		}
		if _, ok := liveIDs[session.ID]; ok {
			continue
		}
		if sessionUpdatedAfter(session, recoveryStartedAt) {
			continue
		}
		d.releaseExitedCrewBinding(session.ID)
		if d.store.GetSessionExit(session.ID) != nil {
			continue
		}
		if d.canReviveSession(session) {
			if session.State == protocol.SessionStateRecoverable {
				continue
			}
			d.applyState(sessionStateChange{
				sessionID: session.ID,
				state:     string(protocol.SessionStateRecoverable),
				cause:     startupRecovery{},
			})
			recoverable++
			continue
		}
		d.removeReapedSession(session.ID)
		removed++
	}
	if recoverable > 0 {
		d.logf("marked %d sessions as recoverable on startup", recoverable)
	}
	return removed
}

func (d *Daemon) canReviveSession(session *protocol.Session) bool {
	if session == nil || d.store == nil {
		return false
	}
	if d.store.SessionCloseIntentional(session.ID) {
		return false
	}
	intent, ok := d.store.LaunchIntent(session.ID)
	if !ok {
		return false
	}
	return d.sessionConversationSurvives(session, intent)
}

func (d *Daemon) sessionConversationSurvives(session *protocol.Session, intent store.LaunchIntent) bool {
	if session.Agent == protocol.AgentShellValue {
		return true
	}
	driver := agentdriver.Get(session.Agent)
	resumeID := agentdriver.ResolveSpawnResumeSessionID(driver, session.ID, "", d.store.GetResumeSessionID(session.ID))
	if d.conversationKnown(driver, resumeID) {
		return true
	}
	return d.store.GetAgentDriverRun(session.ID).RunID != "" ||
		strings.TrimSpace(d.store.GetAgentMetadata(session.ID)) != ""
}

func (d *Daemon) pluginDriverReportsState(agent protocol.SessionAgent) bool {
	if d.plugins == nil {
		return false
	}
	driver, ok := d.plugins.driver(string(agent))
	return ok && driver.Capabilities["state_reporting"]
}

func (d *Daemon) performStartupPTYRecovery(previousRunSessions map[protocol.SessionID]struct{}, recoveryStartedAt time.Time) {

	recoveryReport, recoverErr := d.recoverPTYBackend(10 * time.Second)
	if recoverErr != nil {
		d.logf("PTY backend recovery failed: %v", recoverErr)
		d.addWarning(warnWorkerRecoveryPartial, fmt.Sprintf("PTY recovery failed: %v", recoverErr))
	} else {
		d.logf(
			"PTY recovery summary: recovered=%d pruned=%d missing=%d failed=%d",
			recoveryReport.Recovered,
			recoveryReport.Pruned,
			recoveryReport.Missing,
			recoveryReport.Failed,
		)
		if recoveryReport.Missing > 0 {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf("PTY recovery skipped %d workers due to transient unavailability.", recoveryReport.Missing),
			)
		}
	}

	if _, ok := d.ptyBackend.(ptybackend.RecoverableRuntime); ok {
		d.reconcileStartupWorkerSessions(recoveryReport, recoverErr, previousRunSessions, recoveryStartedAt)
		d.restoreTranscriptWatchers()
		d.pruneRuntimesWithoutSession(context.Background())
		return
	}

	removedSessions := d.pruneSessionsWithoutPTY(previousRunSessions, recoveryStartedAt)
	if removedSessions > 0 {
		d.logf("pruned %d stale sessions without live PTY on startup", removedSessions)
		d.addWarning(
			warnStaleSessionsPruned,
			fmt.Sprintf("Removed %d stale sessions from a previous daemon run because no live PTY was found.", removedSessions),
		)
	}
	d.pruneRuntimesWithoutSession(context.Background())
	d.restoreTranscriptWatchers()
}

func (d *Daemon) pruneRuntimesWithoutSession(ctx context.Context) {
	if d.store == nil {
		return
	}
	for terminal := range d.liveTerminals(ctx) {
		if _, shown := d.shownIn(terminal); shown {
			continue
		}
		if err := d.removePTYSession(terminal); err != nil {
			d.logf("pruning terminal %s without a session failed: %v", terminal, err)
		}
	}
}

func (d *Daemon) recoverPTYBackend(timeout time.Duration) (ptybackend.RecoveryReport, error) {
	recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), timeout)
	defer cancelRecovery()
	return d.ptyBackend.Recover(recoveryCtx)
}

func (d *Daemon) reconcileStartupWorkerSessions(recoveryReport ptybackend.RecoveryReport, recoverErr error, previousRunSessions map[protocol.SessionID]struct{}, recoveryStartedAt time.Time) {
	allowIdleDemotion := recoverErr == nil && recoveryReport.Missing == 0 && recoveryReport.Failed == 0
	if !allowIdleDemotion {
		for attempt := 1; attempt <= startupRecoveryRetryMax; attempt++ {
			// A recovery cut short would read as no live PTYs; stop leaves reconciliation to the next daemon.
			if d.stopping() {
				return
			}
			retryReport, retryErr := d.recoverPTYBackend(5 * time.Second)
			if retryErr == nil && retryReport.Missing == 0 && retryReport.Failed == 0 {
				recoveryReport = retryReport
				recoverErr = nil
				allowIdleDemotion = true
				d.logf(
					"PTY recovery stabilized after retry %d: recovered=%d pruned=%d missing=%d failed=%d",
					attempt,
					retryReport.Recovered,
					retryReport.Pruned,
					retryReport.Missing,
					retryReport.Failed,
				)
				break
			}
			d.logf("PTY recovery retry %d incomplete: err=%v missing=%d failed=%d", attempt, retryErr, retryReport.Missing, retryReport.Failed)
			if attempt < startupRecoveryRetryMax {
				time.Sleep(startupRecoveryRetryDelay)
			}
		}
	}

	reconcile := d.reconcileSessionsWithWorkerBackend(context.Background(), allowIdleDemotion, previousRunSessions, recoveryStartedAt)
	if reconcile.Created > 0 || reconcile.StateUpdated > 0 || reconcile.MarkedIdle > 0 || reconcile.MarkedRecoverable > 0 || reconcile.Reaped > 0 || reconcile.SkippedIdle > 0 || reconcile.SkippedRecent > 0 || reconcile.SkippedShell > 0 || reconcile.LikelyAlive > 0 || reconcile.LivenessUnknown > 0 || reconcile.MissingMetadata > 0 {
		d.logf(
			"worker session reconciliation summary: created=%d state_updated=%d marked_idle=%d marked_recoverable=%d reaped=%d skipped_idle=%d skipped_recent=%d skipped_shell=%d likely_alive=%d liveness_unknown=%d missing_metadata=%d",
			reconcile.Created,
			reconcile.StateUpdated,
			reconcile.MarkedIdle,
			reconcile.MarkedRecoverable,
			reconcile.Reaped,
			reconcile.SkippedIdle,
			reconcile.SkippedRecent,
			reconcile.SkippedShell,
			reconcile.LikelyAlive,
			reconcile.LivenessUnknown,
			reconcile.MissingMetadata,
		)
	}
	if reconcile.SkippedIdle > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Deferred marking %d tracked sessions idle because PTY recovery was incomplete.", reconcile.SkippedIdle),
		)
	}
	if reconcile.MarkedRecoverable > 0 {
		d.addWarning(
			warnStaleSessionMissingWorker,
			fmt.Sprintf("%d sessions can be recovered from a previous daemon run.", reconcile.MarkedRecoverable),
		)
	}
	if reconcile.Reaped > 0 {
		d.addWarning(
			warnStaleSessionsPruned,
			fmt.Sprintf("Removed %d non-recoverable sessions from a previous daemon run.", reconcile.Reaped),
		)
	}
	if reconcile.MarkedIdle > 0 {
		d.addWarning(
			warnStaleSessionMissingWorker,
			fmt.Sprintf("%d tracked sessions were expected to be running but no worker was recovered; they were marked idle.", reconcile.MarkedIdle),
		)
	}
	if reconcile.MissingMetadata > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Recovered workers were missing metadata for %d sessions.", reconcile.MissingMetadata),
		)
	}
	if reconcile.LikelyAlive > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Retained %d sessions in non-idle state because worker liveness signals were still present.", reconcile.LikelyAlive),
		)
	}
	if reconcile.LivenessUnknown > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Retained %d sessions in non-idle state because worker liveness checks were inconclusive.", reconcile.LivenessUnknown),
		)
	}
	if reconcile.SkippedRecent > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Retained %d sessions in non-idle state because they were updated after recovery started.", reconcile.SkippedRecent),
		)
	}
	if reconcile.SkippedIdle > 0 || reconcile.SkippedRecent > 0 || reconcile.LivenessUnknown > 0 || reconcile.MissingMetadata > 0 {
		d.scheduleDeferredWorkerReconciliation(previousRunSessions, recoveryStartedAt)
	}
}

func (d *Daemon) reconcileSessionsWithWorkerBackend(ctx context.Context, allowIdleDemotion bool, previousRunSessions map[protocol.SessionID]struct{}, recoveryStartedAt time.Time) workerReconcileReport {
	return d.reconcileSessionsWithWorkerBackendState(ctx, allowIdleDemotion, allowIdleDemotion, previousRunSessions, recoveryStartedAt)
}

func (d *Daemon) reconcileSessionsWithWorkerBackendState(ctx context.Context, allowIdleDemotion, allowTombstoneCleanup bool, previousRunSessions map[protocol.SessionID]struct{}, recoveryStartedAt time.Time) workerReconcileReport {
	report := workerReconcileReport{}
	if d.store == nil || d.ptyBackend == nil {
		return report
	}

	liveTerminals := d.liveTerminals(ctx)
	liveIDs := make(map[protocol.SessionID]struct{}, len(liveTerminals))
	shownBy := make(map[protocol.SessionID]harness.TerminalID, len(liveTerminals))
	placed := make(map[harness.TerminalID]bool, len(liveTerminals))
	for terminalID := range liveTerminals {
		sessionID, isPlaced := d.terminals().Showing(terminalID)
		if !isPlaced {
			d.logf("worker reconciliation left runtime %s to the orphan prune: no pane places it", terminalID)
			continue
		}
		liveIDs[sessionID] = struct{}{}
		shownBy[sessionID] = terminalID
		placed[terminalID] = isPlaced
	}

	infoProvider, _ := d.ptyBackend.(ptybackend.SessionInfoProvider)
	livenessProber, _ := d.ptyBackend.(ptybackend.SessionLivenessProber)

	for sessionID, terminalID := range shownBy {
		existing := d.store.Get(sessionID)
		intentionalClose, intentErr := d.store.SessionCloseIntentionalChecked(sessionID)
		if intentErr != nil {
			d.logf("worker reconciliation skipped session %s: %v", sessionID, intentErr)
			report.LivenessUnknown++
			continue
		}
		if intentionalClose {
			teardown := d.resumeSessionTeardown(sessionID)
			if teardown != nil {
				d.terminateSessionAsync(sessionID, syscall.SIGTERM, teardown)
			}
			if existing != nil {
				report.Reaped++
				report.markChanged(sessionID)
			}
			continue
		}
		if existing == nil && d.store.SessionClosed(sessionID) {
			d.logf("worker reconciliation stopped runtime %s: its session is closed", sessionID)
			d.terminateSession(sessionID, syscall.SIGTERM)
			continue
		}

		var info ptybackend.SessionInfo
		var haveInfo bool
		if infoProvider != nil {
			fetched, err := infoProvider.SessionInfo(ctx, terminalID)
			if err == nil {
				info = fetched
				haveInfo = true
			}
		}

		if existing == nil {
			if !placed[terminalID] {
				d.logf("worker reconciliation left runtime %s to the orphan prune: no pane places it", terminalID)
				continue
			}
			if !haveInfo {
				report.MissingMetadata++
				continue
			}
			if normalizeSpawnAgent(info.Agent) == protocol.AgentShellValue {
				report.SkippedShell++
				continue
			}

			now := string(protocol.TimestampNow())
			directory := strings.TrimSpace(info.CWD)
			if directory == "" {
				report.MissingMetadata++
				continue
			}
			label := filepath.Base(directory)
			if label == "" || label == "." || label == string(filepath.Separator) {
				label = string(sessionID)
			}

			state, ok := sessionStateFromRecoveredInfo(info)
			if !ok {
				state = protocol.SessionStateLaunching
			}

			profile, err := d.store.MostRecentlyUsedProfile()
			if err != nil {
				d.logf("worker reconciliation left runtime %s without a session row: no profile to adopt it into: %v", sessionID, err)
				report.MissingMetadata++
				continue
			}
			recoveredSession := &protocol.Session{
				ID:             sessionID,
				Label:          label,
				Agent:          normalizeStoredSessionAgent(info.Agent, protocol.SessionAgentCodex),
				Directory:      directory,
				ProfileID:      profile.ID,
				State:          state,
				StateSince:     now,
				StateUpdatedAt: now,
				LastSeen:       now,
			}
			if err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(foregroundCleanupProtection) error {
				if err := d.store.AddChecked(recoveredSession); err != nil {
					return err
				}
				return d.placeLaunchedSession(recoveredSession, &launchPlacement{reopen: true}).err
			}); err != nil {
				d.logf("worker reconciliation could not adopt runtime %s: %v", sessionID, err)
				report.MissingMetadata++
				continue
			}
			report.Created++
			report.markChanged(sessionID)
			continue
		}

		d.store.Touch(sessionID)
		d.store.ClearSessionIntentionalClose(sessionID)

		if existing.State == protocol.SessionStateScheduled {
			continue
		}
		if haveInfo {
			if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" &&
				(d.pluginDriverReportsState(existing.Agent) ||
					existing.State == protocol.SessionStateWaitingInput ||
					existing.State == protocol.SessionStatePendingApproval) {
				continue
			}
			nextState, ok := sessionStateFromRecoveredInfo(info)
			if !ok && !resolverOwnedStates[existing.State] {
				nextState, ok = protocol.SessionStateLaunching, true
			}
			if ok && existing.State != nextState {
				d.applyState(sessionStateChange{
					sessionID: sessionID,
					state:     string(nextState),
					cause:     startupRecovery{},
				})
				report.StateUpdated++
				report.markChanged(sessionID)
			}
			d.seedRecoveredEvidence(sessionID, existing, info)
			continue
		}
	}
	if allowTombstoneCleanup {
		for _, sessionID := range d.store.SessionTeardownIntentIDs() {
			if _, live := liveIDs[sessionID]; live {
				continue
			}
			if d.sessionTeardownInFlight(sessionID) {
				continue
			}
			existing := d.store.Get(sessionID)
			teardown := d.resumeSessionTeardown(sessionID)
			if teardown != nil {
				if !d.notifyPreparedPluginDriverSessionClosed(sessionID, teardown, syscall.SIGTERM) {
					continue
				}
				d.store.ClearSessionIntentionalClose(sessionID)
			}
			if existing != nil {
				report.Reaped++
				report.markChanged(sessionID)
			}
		}
	}

	for _, session := range d.store.List("") {
		if _, fromPreviousRun := previousRunSessions[session.ID]; !fromPreviousRun {
			continue
		}
		if _, ok := liveIDs[session.ID]; ok {
			continue
		}
		if sessionUpdatedAfter(session, recoveryStartedAt) {
			report.SkippedRecent++
			continue
		}
		if livenessProber != nil {
			likelyAlive, probeErr := livenessProber.SessionLikelyAlive(ctx, d.primaryTerminal(session.ID))
			if probeErr != nil {
				d.logf("worker liveness probe failed for session %s: %v", session.ID, probeErr)
				report.LivenessUnknown++
				continue
			}
			if likelyAlive {
				report.LikelyAlive++
				continue
			}
		}
		if !allowIdleDemotion {
			report.SkippedIdle++
			continue
		}
		d.releaseExitedCrewBinding(session.ID)
		if d.store.GetSessionExit(session.ID) != nil {
			continue
		}
		if d.canReviveSession(session) {
			if session.State == protocol.SessionStateRecoverable {
				continue
			}
			d.applyState(sessionStateChange{
				sessionID: session.ID,
				state:     string(protocol.SessionStateRecoverable),
				cause:     startupRecovery{},
			})
			report.StateUpdated++
			report.MarkedRecoverable++
			report.markChanged(session.ID)
		} else {
			d.removeReapedSession(session.ID)
			report.Reaped++
			report.markChanged(session.ID)
		}
	}

	return report
}

func (d *Daemon) scheduleDeferredWorkerReconciliation(previousRunSessions map[protocol.SessionID]struct{}, recoveryStartedAt time.Time) {
	d.life.Go("runDeferredWorkerReconciliation", func() {
		d.runDeferredWorkerReconciliation(deferredRecoveryMaxAttempts, deferredRecoveryRetryInterval, previousRunSessions, recoveryStartedAt)
	})
}

func (d *Daemon) runDeferredWorkerReconciliation(maxAttempts int, retryInterval time.Duration, previousRunSessions map[protocol.SessionID]struct{}, recoveryStartedAt time.Time) {
	if d.ptyBackend == nil || maxAttempts <= 0 {
		return
	}
	if _, ok := d.ptyBackend.(ptybackend.RecoverableRuntime); !ok {
		return
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-d.life.Done():
			return
		default:
		}
		if attempt > 1 && retryInterval > 0 {
			select {
			case <-d.life.Done():
				return
			case <-time.After(retryInterval):
			}
		}

		recoveryCtx, cancel := context.WithTimeout(context.Background(), deferredRecoveryRPCTimeout)
		recoveryReport, recoverErr := d.ptyBackend.Recover(recoveryCtx)
		cancel()

		fullyRecovered := recoverErr == nil && recoveryReport.Missing == 0 && recoveryReport.Failed == 0
		forceIdleDemotion := attempt == maxAttempts
		if !fullyRecovered && !forceIdleDemotion {
			d.logf("deferred PTY recovery attempt %d incomplete: err=%v missing=%d failed=%d", attempt, recoverErr, recoveryReport.Missing, recoveryReport.Failed)
			continue
		}

		reconcile := d.reconcileSessionsWithWorkerBackendState(context.Background(), true, fullyRecovered, previousRunSessions, recoveryStartedAt)
		d.publishSessionsReconciled(reconcile)
		if reconcile.MarkedRecoverable > 0 {
			d.addWarning(
				warnStaleSessionMissingWorker,
				fmt.Sprintf("%d sessions can be recovered from a previous daemon run.", reconcile.MarkedRecoverable),
			)
		}
		if reconcile.Reaped > 0 {
			d.addWarning(
				warnStaleSessionsPruned,
				fmt.Sprintf("Removed %d non-recoverable sessions from a previous daemon run.", reconcile.Reaped),
			)
		}
		if reconcile.MarkedIdle > 0 {
			d.addWarning(
				warnStaleSessionMissingWorker,
				fmt.Sprintf("%d tracked sessions were expected to be running but no worker was recovered; they were marked idle.", reconcile.MarkedIdle),
			)
		}
		if !fullyRecovered {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf(
					"Forced stale-session reconciliation after %d deferred PTY recovery attempts (missing=%d failed=%d).",
					maxAttempts,
					recoveryReport.Missing,
					recoveryReport.Failed,
				),
			)
		}
		if reconcile.LivenessUnknown > 0 {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf("Deferred stale-session idle demotion for %d sessions because liveness checks remained inconclusive.", reconcile.LivenessUnknown),
			)
		}
		if reconcile.SkippedRecent > 0 {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf("Deferred stale-session idle demotion for %d sessions that were updated after recovery began.", reconcile.SkippedRecent),
			)
		} else if reconcile.MarkedIdle > 0 || reconcile.MarkedRecoverable > 0 || reconcile.Reaped > 0 {
			d.logf("deferred worker reconciliation: marked_idle=%d marked_recoverable=%d reaped=%d", reconcile.MarkedIdle, reconcile.MarkedRecoverable, reconcile.Reaped)
		}
		return
	}
}

func stampSessionTimestamps(session *protocol.Session, now string) {
	if strings.TrimSpace(session.StateSince) == "" {
		session.StateSince = now
	}
	if strings.TrimSpace(session.StateUpdatedAt) == "" {
		session.StateUpdatedAt = now
	}
	if strings.TrimSpace(session.LastSeen) == "" {
		session.LastSeen = now
	}
}

func sessionUpdatedAfter(session *protocol.Session, cutoff time.Time) bool {
	if session == nil || cutoff.IsZero() {
		return false
	}
	updatedAt := protocol.Timestamp(session.StateUpdatedAt).Time()
	if updatedAt.IsZero() {
		return false
	}
	return updatedAt.After(cutoff)
}

func normalizeStoredSessionAgent(agent string, fallback protocol.SessionAgent) protocol.SessionAgent {
	normalized := strings.TrimSpace(strings.ToLower(agent))
	if normalized == "" {
		return protocol.NormalizeSessionAgent(fallback, protocol.SessionAgentCodex)
	}
	if normalized == protocol.AgentShellValue {
		return protocol.SessionAgentShell
	}
	if agentdriver.Get(normalized) != nil {
		return protocol.SessionAgent(normalized)
	}
	if normalizePluginAgent(normalized) != "" {
		return protocol.SessionAgent(normalized)
	}
	return protocol.NormalizeSessionAgent(fallback, protocol.SessionAgentCodex)
}

func sessionStateFromRecoveredInfo(info ptybackend.SessionInfo) (protocol.SessionState, bool) {
	if !info.Running {
		return protocol.SessionStateIdle, true
	}
	agent := normalizeStoredSessionAgent(info.Agent, protocol.SessionAgentCodex)
	return agentdriver.RecoveredRunningSessionState(agentdriver.Get(string(agent)), info.State)
}

func (d *Daemon) Stop() {
	d.stopOnce.Do(d.stop)
}

func (d *Daemon) stop() {
	d.log("daemon stopping")
	d.life.end()
	if d.listener != nil {
		d.listener.Close()
		d.listener = nil
		os.Remove(d.socketPath)
	}
	d.wsHub.closeAll()
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.httpServer.Shutdown(ctx)
	}
	if d.httpListener != nil {
		d.httpListener.Close()
	}
	if d.diagServer != nil {
		_ = d.diagServer.Close()
	}
	// Work begun before the stop finishes; later exits and failures are the teardown's, not outcomes.
	// PTYs shut next so no real exit lands between the drain and the shutdown.
	d.life.wait(d.logf)
	d.closeGitExecution(ErrGitExecutorClosed)
	if d.ptyBackend != nil {
		_ = d.ptyBackend.Shutdown(context.Background())
	}
	d.sessionInputs().stopRetries()
	d.stopNotebookWatcher()
	d.stopFsWatchers()
	if d.hubManager != nil {
		d.hubManager.Stop()
	}
	d.stopEventBus()
	d.stopInstalledPlugins()
	d.stopAllTranscriptWatchers()

	d.stopInbox()
	d.pluginDriverSilence().stop()
	d.stopAutoSettleTimers()
	// Store.Close drains connections and checkpoints the WAL so attn.db alone holds every commit.
	if d.store != nil {
		if err := d.store.Close(); err != nil {
			d.logf("close database: %v", err)
		}
	}
	d.releasePIDLock()
	if d.logger != nil {
		d.logger.Close()
	}
}

var errDaemonStopping = errors.New("the daemon is stopping; retry once it is back")

func (d *Daemon) handlePTYExit(info ptybackend.ExitInfo) bool {
	release, held := d.life.Hold("handlePTYExit")
	if !held {
		d.logf("pty exit of %s during daemon stop is not an outcome; the next daemon recovers the session", info.ID)
		return false
	}
	defer release()
	if d.consumeTerminalExitIntent(info.ID, terminalExitReload) {
		d.logf("suppressing exit for reloading terminal %s (runtime replaced in place)", info.ID)
		return false
	}
	sessionID, shown := d.shownIn(info.ID)
	if ended, ending := d.terminals().takeEnding(info.ID); ending && !shown {
		sessionID, shown = ended, true
	}
	if !shown {
		d.clearTerminalExitIntent(info.ID, terminalExitStop)
		d.logf("pty exit of terminal %s, which no session shows; removing its runtime", info.ID)
		if err := d.removePTYSession(info.ID); err != nil {
			d.logf("pty backend remove on exit failed for %s: %v", info.ID, err)
		}
		return false
	}
	if d.queueExitDuringPluginLaunch(sessionID, info) {
		return false
	}
	if d.supersededExitDuringPluginLaunch(sessionID, info) {
		if activeRun := d.store.GetAgentDriverRun(sessionID); activeRun.RunID == info.LifecycleID {
			d.closePluginDriverSession(sessionID, "exited", &info.ExitCode, info.Signal)
		}
		return false
	}
	if info.LifecycleID != "" {
		activeRun := d.store.GetAgentDriverRun(sessionID)
		if activeRun.RunID != "" && activeRun.RunID != info.LifecycleID {
			d.logf("ignoring stale plugin PTY exit: session=%s exited_run=%s active_run=%s", sessionID, info.LifecycleID, activeRun.RunID)
			return false
		}
	}
	stopped := d.consumeTerminalExitIntent(info.ID, terminalExitStop)
	if !d.endTerminal(sessionID, info.ID) {
		d.logf("terminal %s exited; session %s runs on in its other terminals", info.ID, sessionID)
		return true
	}
	d.sessionInputs().forgetSession(sessionID)
	d.stopTranscriptWatcher(sessionID)
	d.closePluginDriverSession(sessionID, "exited", &info.ExitCode, info.Signal)
	d.captureExitScreen(sessionID, info)
	d.noteLaunchExited(sessionID, info)

	if d.ptyBackend != nil {
		if err := d.removePTYSession(info.ID); err != nil {
			d.logf("pty backend remove on exit failed for %s: %v", info.ID, err)
		}
	}
	d.releaseExitedCrewBinding(sessionID)

	d.publishFact(FactSessionPTYExited, string(sessionID), ptyExit{
		Terminal: string(info.ID),
		ExitCode: info.ExitCode,
		Signal:   info.Signal,
	})
	d.recordProcessEvidence(sessionID, true)
	d.broadcastSessionStateChanged(sessionID)
	if info.ExitCode == 0 && info.Signal == "" && !stopped && d.sessionCloseError(sessionID) == nil {
		closing, err := d.beginSessionClose(sessionID, store.SessionClose{By: string(sessionID), Reason: "Agent exited normally"}, nil)
		if err != nil {
			d.logf("closing normally exited session %s: %v", sessionID, err)
		} else {
			d.finishSessionClose(sessionID, closing)
		}
	}
	return true
}

type ptyExit struct {
	Terminal string `json:"terminal,omitempty"`
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`
}

func (d *Daemon) projectSessionPTYExited(ev bus.Event) {
	exit, ok := decodeFact[ptyExit](d, ev)
	if !ok {
		return
	}
	terminal := exit.Terminal
	if terminal == "" {
		terminal = ev.Subject
	}
	event := &protocol.WebSocketEvent{
		Event:     protocol.EventSessionExited,
		ID:        protocol.Ptr(terminal),
		SessionID: protocol.Ptr(protocol.SessionID(ev.Subject)),
		ExitCode:  protocol.Ptr(exit.ExitCode),
	}
	if exit.Signal != "" {
		event.Signal = protocol.Ptr(exit.Signal)
	}
	d.wsHub.Broadcast(event)
}

func (d *Daemon) removePTYSession(terminal harness.TerminalID) error {
	if d.ptyBackend == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := d.ptyBackend.Remove(ctx, terminal)
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	d.life.Go("removePTYSession", func() {
		backoff := 250 * time.Millisecond
		for i := 0; i < 4; i++ {
			select {
			case <-time.After(backoff):
			case <-d.life.Done():
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			retryErr := d.ptyBackend.Remove(ctx, terminal)
			cancel()
			if retryErr == nil || errors.Is(retryErr, pty.ErrSessionNotFound) || errors.Is(retryErr, os.ErrNotExist) {
				return
			}
			backoff *= 2
		}
		d.logf("pty backend remove still failing after retries for %s: %v", terminal, err)
	})
	return err
}

type sessionTeardown struct {
	session *protocol.Session
	// Captured before the pane closes: teardown kills the terminals the session showed.
	terminals        []harness.TerminalID
	driverRun        store.AgentDriverReportCursor
	lifecycleLock    *sessionLockLease
	lifecycleRelease sync.Once
}

func (t *sessionTeardown) releaseLifecycle() {
	if t == nil || t.lifecycleLock == nil {
		return
	}
	t.lifecycleRelease.Do(t.lifecycleLock.Unlock)
}

func (d *Daemon) terminateSession(sessionID protocol.SessionID, sig syscall.Signal) {
	if err := d.terminateSessionChecked(sessionID, sig); err != nil {
		d.logf("session teardown failed for %s: requested=%s error=%v", sessionID, signalName(sig), err)
		d.markForcedStopClassification(sessionID)
		if d.store != nil {
			if markErr := d.store.MarkSessionIntentionalClose(sessionID, time.Now()); markErr != nil {
				d.logf("session teardown intent failed for %s: %v", sessionID, markErr)
			}
		}
		d.stopTranscriptWatcher(sessionID)
		if d.ptyBackend != nil {
			for _, terminal := range d.terminalsOf(sessionID) {
				_ = d.ptyBackend.Remove(context.Background(), terminal)
			}
		}
	}
}

func (d *Daemon) markSessionTerminationIntent(sessionID protocol.SessionID) error {
	d.markForcedStopClassification(sessionID)
	if d.store != nil {
		if err := d.store.MarkSessionIntentionalClose(sessionID, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) terminateSessionChecked(sessionID protocol.SessionID, sig syscall.Signal) error {
	if err := d.markSessionTerminationIntent(sessionID); err != nil {
		return err
	}
	if err := d.terminateSessionRuntimeChecked(sessionID, d.terminalsOf(sessionID), sig); err != nil {
		d.clearForcedStopClassification(sessionID)
		if d.store != nil {
			d.store.ClearSessionIntentionalClose(sessionID)
		}
		return err
	}
	d.closePluginDriverSession(sessionID, "killed", nil, signalName(sig))
	return nil
}

func (d *Daemon) terminateSessionRuntimeChecked(sessionID protocol.SessionID, terminals []harness.TerminalID, sig syscall.Signal) error {
	if d.ptyBackend == nil {
		d.stopTranscriptWatcher(sessionID)
		return nil
	}
	for _, terminal := range terminals {
		d.terminals().noteEnding(terminal, sessionID, time.Now())
		err := d.ptyBackend.Kill(context.Background(), terminal, sig)
		if err != nil {
			d.terminals().takeEnding(terminal)
		}
		if err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
			return err
		}
	}
	d.stopTranscriptWatcher(sessionID)
	for _, terminal := range terminals {
		if err := d.ptyBackend.Remove(context.Background(), terminal); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (d *Daemon) unregisterSession(sessionID protocol.SessionID, sig syscall.Signal) *protocol.Session {
	session := d.store.Get(sessionID)
	if session == nil && d.hubManager != nil {
		session = d.hubManager.RemoteSession(sessionID)
	}
	if session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before session removal: %v", sessionID, err)
		}
	}
	d.terminateSession(sessionID, sig)
	d.removeReapedSession(sessionID)
	return session
}

func (d *Daemon) prepareSessionTeardown(sessionID protocol.SessionID) (*sessionTeardown, error) {
	lifecycleLock := d.sessionLifecycleLockFor(sessionID)
	lifecycleLock.Lock()
	session := d.store.Get(sessionID)
	if session == nil && d.hubManager != nil {
		session = d.hubManager.RemoteSession(sessionID)
	}
	if session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before session removal: %v", sessionID, err)
		}
	}
	d.markForcedStopClassification(sessionID)
	if d.prepareSessionTeardownHook != nil {
		if err := d.prepareSessionTeardownHook(string(sessionID)); err != nil {
			d.clearForcedStopClassification(sessionID)
			lifecycleLock.Unlock()
			return nil, err
		}
	}
	driverRun, err := d.store.PrepareSessionTeardown(sessionID, d.primaryTerminal(sessionID), time.Now())
	if err != nil {
		d.clearForcedStopClassification(sessionID)
		lifecycleLock.Unlock()
		return nil, err
	}
	return &sessionTeardown{session: session, terminals: d.terminalsOf(sessionID), driverRun: driverRun, lifecycleLock: lifecycleLock}, nil
}

func (d *Daemon) commitSessionUnregister(sessionID protocol.SessionID, closed store.SessionClose) {
	d.closeSession(sessionID, closed)
}

func (d *Daemon) cancelSessionTeardown(sessionID protocol.SessionID, teardown *sessionTeardown) {
	defer teardown.releaseLifecycle()
	d.clearForcedStopClassification(sessionID)
	if err := d.store.CancelSessionTeardown(sessionID); err != nil {
		d.logf("cancel session teardown failed for %s: %v", sessionID, err)
	}
}

func (d *Daemon) resumeSessionTeardown(sessionID protocol.SessionID) *sessionTeardown {
	driverRun, found, err := d.store.PrepareExistingSessionTeardown(sessionID, time.Now())
	if err != nil {
		d.logf("session teardown recovery failed for %s: %v", sessionID, err)
		return nil
	}
	if !found {
		return nil
	}
	session := d.store.Get(sessionID)
	terminals := d.terminalsOf(sessionID)
	d.closeSession(sessionID, store.SessionClose{By: store.SessionClosedByUser})
	return &sessionTeardown{session: session, terminals: terminals, driverRun: driverRun}
}

func (d *Daemon) notifyPreparedPluginDriverSessionClosed(sessionID protocol.SessionID, teardown *sessionTeardown, sig syscall.Signal) bool {
	run := teardown.driverRun
	if run.RunID == "" {
		return true
	}
	crashAt("session-close-before-driver-notification")
	claimed, err := d.store.ClaimSessionTeardownDriverRun(sessionID, run.RunID)
	if err != nil {
		d.logf("plugin session close claim failed: session=%s run=%s error=%v", sessionID, run.RunID, err)
		return false
	}
	if claimed {
		if run.TerminalID != "" {
			d.notifyPluginDriverSessionClosed(run.PluginName, run.TerminalID, run.RunID, "killed", nil, signalName(sig))
		} else {
			d.logf("plugin session close: legacy teardown has no terminal: session=%s run=%s", sessionID, run.RunID)
		}
	}
	return true
}

func (d *Daemon) sessionTeardownInFlight(sessionID protocol.SessionID) bool {
	d.teardownMu.Lock()
	defer d.teardownMu.Unlock()
	return d.tearingDown[sessionID] != nil
}

func (d *Daemon) waitForSessionTeardown(sessionID protocol.SessionID) {
	d.teardownMu.Lock()
	done := d.tearingDown[sessionID]
	d.teardownMu.Unlock()
	if done != nil {
		<-done
	}
}

func (d *Daemon) terminateSessionAsync(sessionID protocol.SessionID, sig syscall.Signal, teardown *sessionTeardown) <-chan struct{} {
	d.teardownMu.Lock()
	if d.tearingDown == nil {
		d.tearingDown = make(map[protocol.SessionID]chan struct{})
	}
	if done := d.tearingDown[sessionID]; done != nil {
		d.teardownMu.Unlock()
		teardown.releaseLifecycle()
		return done
	}
	done := make(chan struct{})
	d.tearingDown[sessionID] = done
	d.teardownMu.Unlock()
	teardown.releaseLifecycle()

	terminate := func() {
		defer func() {
			d.teardownMu.Lock()
			delete(d.tearingDown, sessionID)
			d.teardownMu.Unlock()
			close(done)
		}()
		if teardown == nil {
			d.terminateSession(sessionID, sig)
			return
		}
		if err := d.terminateSessionRuntimeChecked(sessionID, teardown.terminals, sig); err != nil {
			d.logf("session teardown failed for %s: requested=%s error=%v", sessionID, signalName(sig), err)
			for _, terminal := range teardown.terminals {
				_ = d.removePTYSession(terminal)
			}
			return
		}
		d.notifyPreparedPluginDriverSessionClosed(sessionID, teardown, sig)
	}
	// A close the user asked for still ends the session while the daemon stops, on the caller's goroutine.
	if !d.life.Go("terminateSessionAsync", terminate) {
		terminate()
	}
	return done
}

func (d *Daemon) closeSession(sessionID protocol.SessionID, closed store.SessionClose) {
	d.recordSessionClose(sessionID, func() (bool, error) {
		return d.store.CloseSession(sessionID, closed, time.Now())
	})
}

func (d *Daemon) restoreSessionClose(sessionID protocol.SessionID, closed store.SessionCloseRecord) {
	d.recordSessionClose(sessionID, func() (bool, error) {
		return d.store.RestoreSessionClose(sessionID, closed)
	})
}

func (d *Daemon) recordSessionClose(sessionID protocol.SessionID, commit func() (bool, error)) {
	defer d.announceUnplacement(sessionID)()
	if session := d.store.Get(sessionID); session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before closing it: %v", sessionID, err)
		}
	}
	defer d.drainTranscriptWatcher(sessionID)()
	d.forgetSessionRuntime(sessionID)
	recorded, err := commit()
	if err != nil {
		d.logf("close session %s: %v", sessionID, err)
	}
	d.forgetSessionTrace(sessionID)
	if recorded {
		d.queueConversationKeep()
		d.invalidateGardenSeedParties("session close")
		entry := d.store.SessionLedgerEntry(sessionID)
		d.decorateLedgerEntryWithUsage(entry)
		d.publishFact(FactSessionClosed, string(sessionID), entry)
	}
	d.clearChiefOfStaffIfSession(sessionID)
	d.releaseCrewBindingIfSession(sessionID)
	d.crewMemo().forget(sessionID)
	if d.hubManager != nil {
		d.hubManager.ForgetSession(sessionID)
	}
	d.clearClassifiedTurn(sessionID)
	d.clearClassifyingTurn(sessionID)
}

func (d *Daemon) removeReapedSession(sessionID protocol.SessionID) {
	defer d.announceUnplacement(sessionID)()
	if session := d.store.Get(sessionID); session != nil {
		if _, err := d.captureGardenSessionExecution(session); err != nil {
			d.logf("garden: preserving execution %s before reaping: %v", sessionID, err)
		}
	}
	d.forgetSessionRuntime(sessionID)
	d.store.Remove(sessionID)
	d.forgetSessionTrace(sessionID)
	d.clearChiefOfStaffIfSession(sessionID)
	d.releaseCrewBindingIfSession(sessionID)
}

func (d *Daemon) forgetSessionRuntime(sessionID protocol.SessionID) {
	d.stopTranscriptWatcher(sessionID)

	d.kickInboxAfterCommit(inbox.ToSession(sessionID))
	d.forgetSessionTitleInitialPrompt(sessionID)
	d.clearAutoSettleState(sessionID)
	d.lastInputMu.Lock()
	delete(d.lastAutoSettleActivityAt, sessionID)
	d.lastInputMu.Unlock()
	d.clearSnoozeState(sessionID)
	d.sessionInputs().forgetSession(sessionID)
	d.forgetPluginDriverSilenceWatch(sessionID)
}

func (d *Daemon) forgetSessionTrace(sessionID protocol.SessionID) {
	d.forgetStateTrace(sessionID)
	d.evidenceTable().forget(sessionID)
	d.sessionResolver().forget(sessionID)
	d.stateReasons().forget(sessionID)
	d.dwellGate().clear(sessionID)
}

func (d *Daemon) handlePTYState(terminal harness.TerminalID, obs pty.Observation) {
	sessionID, shown := d.shownIn(terminal)
	if !shown {
		return
	}
	state := obs.Claim
	origin := stateOrigin{source: string(obs.Source), detail: obs.Detail, observedAt: obs.At}
	evidenceChanged := d.recordPTYEvidence(sessionID, obs)
	if !obs.Source.ClaimsProtocolState() {
		if evidenceChanged {
			d.traceStateEvidence(sessionID, origin, state)
		}
		return
	}
	session := d.store.Get(sessionID)
	if session == nil {
		d.traceStateVeto(sessionID, origin, state, "session_not_found")
		return
	}
	if d.linkOwnsState(sessionID) {
		d.traceStateVeto(sessionID, origin, state, "link_owns_state")
		return
	}
	if session.State != protocol.SessionStateLaunching {
		reason := "resolver_owned"
		if d.store.GetAgentDriverRun(sessionID).RunID != "" {
			reason = "plugin_driver_not_registered"
		}
		d.traceStateVeto(sessionID, origin, state, reason)
		return
	}
	agent := session.Agent
	d.logf(
		"pty state update: session=%s agent=%s state=%s source=%s detail=%q observed_at=%s",
		sessionID, agent, state, obs.Source, obs.Detail, obs.At.Format(time.RFC3339Nano),
	)
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     state,
		cause:     liveSignal{},
		origin:    origin,
	})
}

func setNoStoreHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store, max-age=0")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
}

func (d *Daemon) initHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", d.handleWS)
	mux.HandleFunc("/health", d.handleHealth)
	mux.HandleFunc("/agents", d.handleAgents)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		setNoStoreHeaders(w.Header())
		w.WriteHeader(http.StatusNoContent)
	})
	d.httpHandler = mux

	d.httpServer = &http.Server{
		Addr:        net.JoinHostPort(config.WSBindAddress(), config.WSPort()),
		Handler:     d.httpHandler,
		ConnContext: withRawConn,
	}
}

func (d *Daemon) listenHTTP() error {
	if d.httpListener != nil {
		return nil
	}
	addr := d.httpServer.Addr
	var listener net.Listener
	var err error
	if rawFD := d.harnessWSListenerFD; rawFD != "" {
		if os.Getenv("ATTN_HARNESS_DATA_DIR") == "" {
			return fmt.Errorf("ATTN_HARNESS_WS_LISTENER_FD requires ATTN_HARNESS_DATA_DIR")
		}
		fd, parseErr := strconv.Atoi(rawFD)
		if parseErr != nil {
			return fmt.Errorf("invalid ATTN_HARNESS_WS_LISTENER_FD %q: %w", rawFD, parseErr)
		}
		file := os.NewFile(uintptr(fd), "harness-websocket-listener")
		listener, err = net.FileListener(file)
		_ = file.Close()
		if err == nil && listener.Addr().String() != addr {
			_ = listener.Close()
			return fmt.Errorf("harness WebSocket listener is %s, want %s", listener.Addr(), addr)
		}
	} else {
		listener, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf(
			"refusing to start: cannot bind the WebSocket address %s for instance %q: %w. "+
				"attn derives that port from the instance name, so the usual owner is a daemon for the same instance running somewhere else — "+
				"notably on a VM whose listener OrbStack forwards onto host localhost while the host port is free. "+
				"Starting anyway would leave this daemon split-brained: the app routes by WebSocket port and would attach to the foreign listener, "+
				"the CLI routes by the unix socket and would talk to this process, and every command sent from the app would silently miss these sessions. "+
				"Free %s (stop whatever holds it, including a forwarding VM) or run this daemon under another instance with ATTN_INSTANCE",
			addr, config.InstanceLabel(), err, addr,
		)
	}
	d.httpListener = listener
	return nil
}

func (d *Daemon) runHTTPServer(listener net.Listener) {
	d.logf("WebSocket server starting on ws://%s/ws", d.httpServer.Addr)
	if err := d.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		d.logf("HTTP server error: %v", err)
	}
}

func (d *Daemon) maybeStartDiagServer() {
	addr, enabled := config.PprofAddr()
	if !enabled {
		return
	}
	srv, err := diag.Start(addr, d.diagStats)
	if err != nil {
		d.logf("diagnostics endpoint failed to start on %s: %v", addr, err)
		return
	}
	d.diagServer = srv
	d.logf("diagnostics endpoint listening on http://%s/ (pprof + /debug/vars)", srv.Addr())
}

func (d *Daemon) diagStats() diag.Stats {
	stats := diag.Stats{PtyBackend: d.ptyBackendMode(), DocSubscriptions: d.documentSubscriptionCount()}
	if d.ptyBackend == nil {
		stats.PtyBackend = "embedded"
		return stats
	}
	ctx := context.Background()
	stats.Sessions = len(d.ptyBackend.TerminalIDs(ctx))
	if wp, ok := d.ptyBackend.(ptybackend.WorkerProcessProvider); ok {
		stats.WorkerPIDs = wp.WorkerPIDs(ctx)
	}
	return stats
}

func (d *Daemon) log(msg string) {
	if d.logger != nil {
		d.logger.Info(msg)
	}
}

func (d *Daemon) logf(format string, args ...interface{}) {
	if d.logger != nil {
		d.logger.Infof(format, args...)
	}
}

func shouldRunWorkerStartupProbe() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_PTY_SKIP_STARTUP_PROBE")))
	switch raw {
	case "1", "true", "yes", "on":
		return false
	default:
		return true
	}
}

func (d *Daemon) refreshGitHubHostsLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-d.life.Done():
			return
		case <-ticker.C:
			if err := d.refreshGitHubHosts(); err != nil {
				d.logf("GitHub host refresh failed: %v", err)
			}
			d.recoverAutomations()
		}
	}
}

func ghVersionWarning(err error) (string, string) {
	if errors.Is(err, exec.ErrNotFound) {
		return warnGHNotInstalled, "GitHub CLI not installed. PR monitoring disabled. " + github.InstallHint()
	}
	var tooOld *github.VersionTooOldError
	if errors.As(err, &tooOld) {
		return warnGHVersionTooOld, "GitHub CLI v" + tooOld.Have + " needs upgrade to v" + tooOld.Want + "+ for PR monitoring. " + github.UpgradeHint()
	}
	return warnGHVersionTooOld, "GitHub CLI needs upgrade to v2.81.0+ for PR monitoring. " + github.UpgradeHint()
}

func (d *Daemon) refreshGitHubHosts() error {
	if d.ghRegistry == nil {
		d.ghRegistry = github.NewClientRegistry()
	}
	hostsBefore := d.gitHubHosts()

	mockURL := strings.TrimSpace(os.Getenv("ATTN_MOCK_GH_URL"))
	if mockURL != "" {
		if err := d.registerMockClient(mockURL); err != nil {
			d.logf("Mock GitHub client not available: %v", err)
		}
		d.broadcastGitHubHosts(hostsBefore)
		return nil
	}

	if reason := gitHubPollingOffReason(); reason != "" {
		if !d.gitHubPollingOffLogged {
			d.gitHubPollingOffLogged = true
			d.logf("%s", reason)
		}
		for _, host := range d.ghRegistry.Hosts() {
			d.ghRegistry.Remove(host)
		}
		d.broadcastGitHubHosts(hostsBefore)
		return nil
	}

	if err := github.RequireGHVersion("2.81.0"); err != nil {
		code, message := ghVersionWarning(err)
		d.logf("gh CLI unavailable (need 2.81.0+): %v", err)
		d.addWarning(code, message)
		return nil
	}

	hosts, err := github.DiscoverHosts(d.life.Context())
	if err != nil {
		d.logf("GitHub host discovery failed: %v", err)
		return nil
	}

	discovered := make(map[string]bool)
	for _, hostInfo := range hosts {
		if hostInfo.Host == "" {
			continue
		}
		token, err := github.GetTokenForHost(d.life.Context(), hostInfo.Host)
		if err != nil {
			d.logf("GitHub token fetch failed for %s: %v", hostInfo.Host, err)
			continue
		}
		client, err := github.NewClientForHost(hostInfo.Host, hostInfo.APIURL, token)
		if err != nil {
			d.logf("GitHub client create failed for %s: %v", hostInfo.Host, err)
			continue
		}
		d.ghRegistry.Register(hostInfo.Host, client)
		discovered[hostInfo.Host] = true
	}
	// Stop cancels the token fetches; hosts it cut short are not gone.
	if d.stopping() {
		return nil
	}

	allowed := make(map[string]bool)
	for host := range discovered {
		allowed[host] = true
	}
	for _, host := range d.ghRegistry.Hosts() {
		if !allowed[host] {
			d.ghRegistry.Remove(host)
		}
	}

	d.broadcastGitHubHosts(hostsBefore)
	return nil
}

func (d *Daemon) gitHubHosts() []string {
	if d.ghRegistry == nil {
		return nil
	}
	return d.ghRegistry.Hosts()
}

func (d *Daemon) broadcastGitHubHosts(before []string) {
	previous := make(map[string]struct{}, len(before))
	for _, host := range before {
		previous[host] = struct{}{}
	}
	current := d.gitHubHosts()
	currentSet := make(map[string]struct{}, len(current))
	for _, host := range current {
		currentSet[host] = struct{}{}
	}

	d.coalesceSnapshots(func() {
		for _, host := range current {
			if _, had := previous[host]; !had {
				d.publishFact(FactGitHubHostAdded, host, nil)
			}
		}
		for _, host := range before {
			if _, still := currentSet[host]; !still {
				d.publishFact(FactGitHubHostRemoved, host, nil)
			}
		}
	})
}

func (d *Daemon) projectGitHubHostsUpdated() {
	d.projectSnapshot(snapshotGHHosts, func() {
		d.wsHub.BroadcastValue(d.gitHubHostsUpdatedMessage())
	})
}

func (d *Daemon) gitHubHostsUpdatedMessage() *protocol.GitHubHostsUpdatedMessage {
	return &protocol.GitHubHostsUpdatedMessage{
		Event:                  protocol.EventGitHubHostsUpdated,
		GithubHosts:            d.gitHubHosts(),
		GithubPollingOffReason: gitHubPollingOffReasonField(),
	}
}

func (d *Daemon) registerMockClient(mockURL string) error {
	token := strings.TrimSpace(os.Getenv("ATTN_MOCK_GH_TOKEN"))
	if token == "" {
		return fmt.Errorf("ATTN_MOCK_GH_TOKEN not set")
	}

	host := strings.TrimSpace(os.Getenv("ATTN_MOCK_GH_HOST"))
	if host == "" {
		host = hostFromURL(mockURL)
	}
	if host == "" {
		host = "mock.github.local"
	}

	client, err := github.NewClientForHost(host, mockURL, token)
	if err != nil {
		return err
	}

	for _, existing := range d.ghRegistry.Hosts() {
		d.ghRegistry.Remove(existing)
	}
	d.ghRegistry.Register(host, client)
	d.logf("Mock GitHub client registered for %s (%s)", host, mockURL)
	return nil
}

func hostFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func (d *Daemon) githubAvailable() bool {
	if d.ghRegistry == nil {
		return false
	}
	return len(d.ghRegistry.Hosts()) > 0
}

func (d *Daemon) clientForPRID(id string) (*github.Client, string, int, string, error) {
	host, repo, number, err := protocol.ParsePRID(id)
	if err != nil {
		return nil, "", 0, "", err
	}
	if d.ghRegistry == nil {
		return nil, "", 0, "", fmt.Errorf("GitHub client not available")
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, "", 0, "", fmt.Errorf("no client for host %s", host)
	}
	return client, repo, number, host, nil
}

func (d *Daemon) acquirePIDLock() error {
	f, err := os.OpenFile(d.pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open PID file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		existingPID := "unknown"
		if data, readErr := os.ReadFile(d.pidPath); readErr == nil {
			if pid := strings.TrimSpace(string(data)); pid != "" {
				existingPID = pid
			}
		}
		f.Close()
		return fmt.Errorf("%w (pid %s)", ErrAlreadyRunning, existingPID)
	}

	f.Truncate(0)
	f.Seek(0, 0)
	pid := os.Getpid()
	if _, err := f.WriteString(strconv.Itoa(pid)); err != nil {
		f.Close()
		return fmt.Errorf("write PID: %w", err)
	}
	f.Sync()

	d.pidFile = f
	d.logf("Acquired PID lock (PID %d, file %s)", pid, d.pidPath)

	return nil
}

func (d *Daemon) releasePIDLock() {
	if d.pidFile != nil {
		syscall.Flock(int(d.pidFile.Fd()), syscall.LOCK_UN)
		d.pidFile.Close()
		d.pidFile = nil
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()
	// Stop ends reads on a command socket at once and gives writes the 10s a WebSocket client gets (defaultWSWriteTimeout),
	// so a finished command still sends its reply but a client that stopped reading cannot hold stop.
	stopReading := context.AfterFunc(d.life.Context(), func() {
		_ = conn.SetReadDeadline(time.Now())
		_ = conn.SetWriteDeadline(time.Now().Add(defaultWSWriteTimeout))
	})
	defer stopReading()

	reader := bufio.NewReader(conn)
	data, err := readInitialSocketFrame(reader, maxInitialSocketFrameBytes)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) && d.stopping() {
			err = errDaemonStopping
		}
		if !errors.Is(err, io.EOF) {
			d.sendError(conn, err.Error())
		}
		return
	}

	helloID, helloParams, pluginMode, err := parsePluginHello(data)
	if pluginMode {
		if err != nil {
			_ = json.NewEncoder(conn).Encode(jsonRPCFailure(helloID, jsonRPCInvalidRequest, err.Error()))
			return
		}
		// A plugin socket stays open until the plugin shuts down, after the wait, so requests to it finish.
		stopReading()
		d.handlePluginConnection(conn, reader, helloID, helloParams)
		return
	}

	cmd, msg, err := protocol.ParseMessage(data)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	release, held := d.life.Hold("handleConnection")
	if !held {
		d.sendError(conn, errDaemonStopping.Error())
		return
	}
	defer release()

	if err := d.scopeGardenRequest(cmd, msg, ""); err != nil {
		d.sendError(conn, err.Error())
		return
	}

	switch cmd {
	case protocol.CmdLaunchDesktopGet, protocol.CmdLaunchDesktopSet:
		d.handleLaunchDesktopCommand(conn, cmd, msg)
	case protocol.CmdDelegate:
		d.handleDelegate(conn, msg.(*protocol.DelegateMessage))
	case protocol.CmdAutomationApply, protocol.CmdAutomationValidate, protocol.CmdAutomationDefinitionsGet, protocol.CmdAutomationDefinitionGet, protocol.CmdAutomationRun, protocol.CmdAutomationRunsGet, protocol.CmdAutomationSetEnabled, protocol.CmdAutomationDelete, protocol.CmdAutomationCleanup:
		d.handleAutomationCommand(conn, cmd, msg)
	case protocol.CmdDelegationRoles:
		d.handleDelegationRoles(conn)
	case protocol.CmdDelegationPreferencesShow:
		d.handleDelegationPreferencesShow(conn)
	case protocol.CmdDelegationPreferencesCommit:
		d.handleDelegationPreferencesCommit(conn, msg.(*protocol.DelegationPreferencesCommitMessage))
	case protocol.CmdDelegationPreferencesHistory:
		d.handleDelegationPreferencesHistory(conn, msg.(*protocol.DelegationPreferencesHistoryMessage))
	case protocol.CmdDelegationPreferencesRollback:
		d.handleDelegationPreferencesRollback(conn, msg.(*protocol.DelegationPreferencesRollbackMessage))
	case protocol.CmdDelegateStatus:
		d.handleDelegateStatus(conn, msg.(*protocol.DelegateStatusMessage))

	case protocol.CmdActivityStatus:
		d.handleActivityStatus(conn, msg.(*protocol.ActivityStatusMessage))
	case protocol.CmdClearSessionActivity:
		d.handleClearSessionActivity(conn, msg.(*protocol.ClearSessionActivityMessage))

	case protocol.CmdDocDefine:
		d.handleDocDefine(conn, msg.(*protocol.DocDefineMessage))
	case protocol.CmdDocUndefine:
		d.handleDocUndefine(conn, msg.(*protocol.DocUndefineMessage))
	case protocol.CmdDocCollections:
		d.handleDocCollections(conn, msg.(*protocol.DocCollectionsMessage))
	case protocol.CmdDocPut:
		d.handleDocPut(conn, msg.(*protocol.DocPutMessage))
	case protocol.CmdDocGet:
		d.handleDocGet(conn, msg.(*protocol.DocGetMessage))
	case protocol.CmdDocDelete:
		d.handleDocDelete(conn, msg.(*protocol.DocDeleteMessage))
	case protocol.CmdDocQuery:
		d.handleDocQuery(conn, msg.(*protocol.DocQueryMessage))
	case protocol.CmdDocCount:
		d.handleDocCount(conn, msg.(*protocol.DocCountMessage))
	case protocol.CmdDocSubscribe:
		d.handleDocSubscribe(conn, msg.(*protocol.DocSubscribeMessage))
	case protocol.CmdAutoModeShow:
		d.handleAutoModeShow(conn, msg.(*protocol.AutoModeShowMessage))
	case protocol.CmdAutoModeEnvSlot:
		d.handleAutoModeEnvSlot(conn, msg.(*protocol.AutoModeEnvSlotMessage))
	case protocol.CmdAutoModeEnvNotes:
		d.handleAutoModeEnvNotes(conn, msg.(*protocol.AutoModeEnvNotesMessage))
	case protocol.CmdAutoModePropose:
		d.handleAutoModePropose(conn, msg.(*protocol.AutoModeProposeMessage))
	case protocol.CmdAutoModeDenials:
		d.handleAutoModeDenials(conn, msg.(*protocol.AutoModeDenialsMessage))

	case protocol.CmdPresentOpen:
		d.handlePresentOpen(conn, msg.(*protocol.PresentOpenMessage))
	case protocol.CmdPresentFeedback:
		d.handlePresentFeedback(conn, msg.(*protocol.PresentFeedbackMessage))

	case protocol.CmdNotebookGuide:
		d.handleNotebookGuide(conn, msg.(*protocol.NotebookGuideMessage))
	case protocol.CmdJournalAppend:
		d.handleJournalAppend(conn, msg.(*protocol.JournalAppendMessage))
	case protocol.CmdUnregister:
		d.handleUnregister(conn, msg.(*protocol.UnregisterMessage))
	case protocol.CmdState:
		d.handleState(conn, msg.(*protocol.StateMessage))
	case protocol.CmdHookNotification:
		d.handleHookNotification(conn, msg.(*protocol.HookNotificationMessage))
	case protocol.CmdHookStopFailure:
		d.handleHookStopFailure(conn, msg.(*protocol.HookStopFailureMessage))
	case protocol.CmdHookCompaction:
		d.handleHookCompaction(conn, msg.(*protocol.HookCompactionMessage))
	case protocol.CmdSetSessionResumeID:
		d.handleObserveAgentConversation(conn, msg.(*protocol.SetSessionResumeIDMessage))
	case protocol.CmdSessionInstructions:
		d.handleSessionInstructions(conn, msg.(*protocol.SessionInstructionsMessage))
	case protocol.CmdSessionTranscript:
		d.handleSessionTranscript(conn, msg.(*protocol.SessionTranscriptMessage))
	case protocol.CmdSessionList:
		d.handleSessionList(conn, msg.(*protocol.SessionListMessage))
	case protocol.CmdSessionShow:
		d.handleSessionShow(conn, msg.(*protocol.SessionShowMessage))
	case protocol.CmdSessionReopen:
		d.handleSessionReopen(conn, msg.(*protocol.SessionReopenMessage))
	case protocol.CmdDesktopMoveSession:
		d.handleDesktopMoveSession(conn, msg.(*protocol.DesktopMoveSessionMessage))
	case protocol.CmdSetSessionPriority:
		priority := msg.(*protocol.SetSessionPriorityMessage)
		if d.forwardedToSessionOwner(conn, protocol.TrimID(priority.SessionID), priority) {
			return
		}
		if err := d.setSessionPriority(priority); err != nil {
			d.sendError(conn, err.Error())
		} else {
			d.sendOK(conn)
		}
	case protocol.CmdRenameSession:
		d.handleRenameSessionConn(conn, msg.(*protocol.RenameSessionMessage))
	case protocol.CmdStateExplain:
		d.handleStateExplain(conn, msg.(*protocol.StateExplainMessage))
	case protocol.CmdAgentPeek:
		d.handleAgentPeek(conn, msg.(*protocol.AgentPeekMessage))

	case protocol.CmdAgentMsg:
		d.handleAgentMsg(conn, msg.(*protocol.AgentMsgMessage))
	case protocol.CmdAgentClose:
		d.handleAgentClose(conn, msg.(*protocol.AgentCloseMessage))
	case protocol.CmdAgentInbox:
		d.handleAgentInbox(conn, msg.(*protocol.AgentInboxMessage))
	case protocol.CmdAgentMsgStatus:
		d.handleAgentMsgStatus(conn, msg.(*protocol.AgentMsgStatusMessage))
	case protocol.CmdSeedPlant:
		d.handleSeedPlant(conn, msg.(*protocol.SeedPlantMessage))
	case protocol.CmdSeedPlot:
		d.handleSeedPlot(conn, msg.(*protocol.SeedPlotMessage))
	case protocol.CmdSeedList:
		d.handleSeedList(conn, msg.(*protocol.SeedListMessage))
	case protocol.CmdSeedSearch:
		d.handleSeedSearch(conn, msg.(*protocol.SeedSearchMessage))
	case protocol.CmdSeedShow:
		d.handleSeedShow(conn, msg.(*protocol.SeedShowMessage))
	case protocol.CmdSeedArtifactTransfer:
		d.handleSeedArtifactTransfer(conn, msg.(*protocol.SeedArtifactTransferMessage))
	case protocol.CmdSeedEdit:
		d.handleSeedEdit(conn, msg.(*protocol.SeedEditMessage))
	case protocol.CmdSeedTransition:
		d.handleSeedTransition(conn, msg.(*protocol.SeedTransitionMessage))
	case protocol.CmdSeedNote:
		d.handleSeedNote(conn, msg.(*protocol.SeedNoteMessage))
	case protocol.CmdSeedNotes:
		d.handleSeedNotes(conn, msg.(*protocol.SeedNotesMessage))
	case protocol.CmdSeedWatch:
		d.handleSeedWatch(conn, msg.(*protocol.SeedWatchMessage))
	case protocol.CmdSeedLink:
		d.handleSeedLink(conn, msg.(*protocol.SeedLinkMessage))
	case protocol.CmdSeedReady:
		d.handleSeedReady(conn, msg.(*protocol.SeedReadyMessage))
	case protocol.CmdSeedReviewStart:
		d.handleSeedReviewStart(conn, msg.(*protocol.SeedReviewStartMessage))
	case protocol.CmdSeedSendToChief:
		d.handleSeedSendToChief(conn, msg.(*protocol.SeedSendToChiefMessage))
	case protocol.CmdSeedReviewShow:
		d.handleSeedReviewShow(conn, msg.(*protocol.SeedReviewShowMessage))
	case protocol.CmdSeedReviewCancel:
		d.handleSeedReviewCancel(conn, msg.(*protocol.SeedReviewCancelMessage))
	case protocol.CmdSeedReviewRetry:
		d.handleSeedReviewRetry(conn, msg.(*protocol.SeedReviewRetryMessage))
	case protocol.CmdSeedReviewKeep:
		d.handleSeedReviewKeep(conn, msg.(*protocol.SeedReviewKeepMessage))
	case protocol.CmdCrewList:
		d.handleCrewList(conn, msg.(*protocol.CrewListMessage))
	case protocol.CmdCrewCharterGet:
		d.handleCrewCharterGet(conn, msg.(*protocol.CrewCharterGetMessage))
	case protocol.CmdCrewCharterSet:
		d.handleCrewCharterSet(conn, msg.(*protocol.CrewCharterSetMessage))
	case protocol.CmdCrewHandoffsGet:
		d.handleCrewHandoffsGet(conn, msg.(*protocol.CrewHandoffsGetMessage))
	case protocol.CmdCrewHandoffGet:
		d.handleCrewHandoffGet(conn, msg.(*protocol.CrewHandoffGetMessage))
	case protocol.CmdCrewWake:
		d.handleCrewWake(conn, msg.(*protocol.CrewWakeMessage))
	case protocol.CmdCrewSleep:
		d.handleCrewSleep(conn, msg.(*protocol.CrewSleepMessage))
	case protocol.CmdCrewSet:
		d.handleCrewSet(conn, msg.(*protocol.CrewSetMessage))
	case protocol.CmdCrewRestart:
		d.handleCrewRestart(conn, msg.(*protocol.CrewRestartMessage))
	case protocol.CmdCrewPrime:
		d.handleCrewPrime(conn, msg.(*protocol.CrewPrimeMessage))
	case protocol.CmdCrewHandoff:
		d.handleCrewHandoff(conn, msg.(*protocol.CrewHandoffMessage))
	case protocol.CmdStop:
		d.handleStop(conn, msg.(*protocol.StopMessage))
	case protocol.CmdFilesEdited:
		d.handleFilesEdited(conn, msg.(*protocol.FilesEditedMessage))
	case protocol.CmdPullRequestCreated:
		d.handlePullRequestCreated(conn, msg.(*protocol.PullRequestCreatedMessage))
	case protocol.CmdPullRequestForget:
		d.handlePullRequestForget(conn, msg.(*protocol.PullRequestForgetMessage))
	case protocol.CmdPullRequestWatch:
		d.handlePullRequestWatch(conn, msg.(*protocol.PullRequestWatchMessage))
	case protocol.CmdPullRequestUnwatch:
		d.handlePullRequestUnwatch(conn, msg.(*protocol.PullRequestUnwatchMessage))
	case protocol.CmdWorkflowRunUpsert:
		d.handleWorkflowRunUpsert(conn, msg.(*protocol.WorkflowRunUpsertMessage))
	case protocol.CmdWorkflowCallUpsert:
		d.handleWorkflowCallUpsert(conn, msg.(*protocol.WorkflowCallUpsertMessage))
	case protocol.CmdWorkflowRunGet:
		d.handleWorkflowRunGet(conn, msg.(*protocol.WorkflowRunGetMessage))
	case protocol.CmdWorkflowRunList:
		d.handleWorkflowRunList(conn, msg.(*protocol.WorkflowRunListMessage))
	case protocol.CmdWorkflowRunCancel:
		d.handleWorkflowRunCancel(conn, msg.(*protocol.WorkflowRunCancelMessage))
	case protocol.CmdQuery:
		d.handleQuery(conn, msg.(*protocol.QueryMessage))
	case protocol.CmdHeartbeat:
		d.handleHeartbeat(conn, msg.(*protocol.HeartbeatMessage))
	case protocol.CmdQueryPRs:
		d.handleQueryPRs(conn, msg.(*protocol.QueryPRsMessage))
	case protocol.CmdMutePR:
		d.handleMutePR(conn, msg.(*protocol.MutePRMessage))
	case protocol.CmdMuteRepo:
		d.handleMuteRepo(conn, msg.(*protocol.MuteRepoMessage))
	case protocol.CmdSetSessionContextWindowCap:
		m := msg.(*protocol.SetSessionContextWindowCapMessage)
		if err := d.setSessionContextWindowCap(m.SessionID, m.Cap); err != nil {
			d.sendError(conn, err.Error())
			return
		}
		d.sendOK(conn)
	case protocol.CmdCollapseRepo:
		d.handleCollapseRepo(conn, msg.(*protocol.CollapseRepoMessage))
	case protocol.CmdQueryRepos:
		d.handleQueryRepos(conn, msg.(*protocol.QueryReposMessage))
	case protocol.CmdQueryAuthors:
		d.handleQueryAuthors(conn, msg.(*protocol.QueryAuthorsMessage))
	case protocol.CmdFetchPRDetails:
		d.handleFetchPRDetails(conn, msg.(*protocol.FetchPRDetailsMessage))
	case protocol.CmdInjectTestPR:
		d.handleInjectTestPR(conn, msg.(*protocol.InjectTestPRMessage))
	case protocol.CmdInjectTestSession:
		d.handleInjectTestSession(conn, msg.(*protocol.InjectTestSessionMessage))
	case protocol.CmdOpenMarkdown:
		d.handleOpenMarkdown(conn, msg.(*protocol.OpenMarkdownMessage))
	case protocol.CmdOpenSeed:
		d.handleOpenSeed(conn, msg.(*protocol.OpenSeedMessage))
	case protocol.CmdOpenSentFiles:
		d.handleOpenSentFiles(conn, msg.(*protocol.OpenSentFilesMessage))
	case protocol.CmdOpenBrowser:
		d.handleOpenBrowser(conn, msg.(*protocol.OpenBrowserMessage))
	case protocol.CmdBrowserControl:
		d.handleBrowserControl(conn, msg.(*protocol.BrowserControlMessage))
	case protocol.CmdListWorktrees:
		d.handleListWorktrees(conn, msg.(*protocol.ListWorktreesMessage))
	case protocol.CmdCreateWorktree:
		d.handleCreateWorktree(conn, msg.(*protocol.CreateWorktreeMessage))
	case protocol.CmdDeleteWorktree:
		d.handleDeleteWorktree(conn, msg.(*protocol.DeleteWorktreeMessage))
	case protocol.CmdKeptConversationList:
		d.handleKeptConversationList(conn, msg.(*protocol.KeptConversationListMessage))
	case protocol.CmdKeptConversationKeep:
		d.handleKeptConversationKeep(conn, msg.(*protocol.KeptConversationKeepMessage))
	case protocol.CmdKeptConversationForget:
		d.handleKeptConversationForget(conn, msg.(*protocol.KeptConversationForgetMessage))
	case protocol.CmdWorktreeList:
		d.handleWorktreeList(conn, msg.(*protocol.WorktreeListMessage))
	case protocol.CmdWorktreeKeep:
		d.handleWorktreeKeep(conn, msg.(*protocol.WorktreeKeepMessage))
	case protocol.CmdWorktreeSweepLog:
		d.handleWorktreeSweepLog(conn, msg.(*protocol.WorktreeSweepLogMessage))
	case protocol.CmdWorktreeRefresh:
		d.handleWorktreeRefresh(conn, msg.(*protocol.WorktreeRefreshMessage))
	default:
		d.sendError(conn, "unknown command")
	}
}

func (d *Daemon) projectSessionEvent(event string, sessionID protocol.SessionID) {
	decorated := d.sessionForBroadcast(d.store.Get(sessionID))
	if decorated == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   event,
		Session: decorated,
	})
}

func (d *Daemon) projectSessionUnregistered(ev bus.Event) {
	session, ok := decodeFact[*protocol.Session](d, ev)
	if !ok || session == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionUnregistered,
		Session: session,
	})
}

func (d *Daemon) publishSessionUnregistered(session *protocol.Session) {
	if session == nil {
		return
	}
	d.invalidateGardenSeedParties("session unregister")
	d.publishFact(FactSessionUnregistered, string(session.ID), d.sessionForBroadcast(session))
}

func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	teardown, err := d.prepareSessionTeardown(msg.ID)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("prepare session teardown: %v", err))
		return
	}
	d.commitSessionUnregister(msg.ID, store.SessionClose{By: store.SessionClosedByUser})
	d.sendOK(conn)

	if teardown != nil && teardown.session != nil {
		d.publishSessionUnregistered(teardown.session)
	}
	if teardown != nil {
		d.terminateSessionAsync(msg.ID, syscall.SIGTERM, teardown)
	}
}

func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	d.logf("hook evidence: id=%s state=%s", sessionID, msg.State)
	d.tracePermissionMode(sessionID, protocol.Deref(msg.PermissionMode))
	d.recordReviewerEvidenceFromPermissionMode(sessionID, protocol.Deref(msg.PermissionMode))
	d.traceStateEvidence(sessionID, stateOrigin{source: stateSourceHook}, msg.State)
	d.recordBracketEvidence(sessionID, msg.State)
	if strings.EqualFold(strings.TrimSpace(protocol.Deref(msg.HookEvent)), "user_prompt_submit") &&
		strings.TrimSpace(protocol.Deref(msg.Prompt)) != "" {
		origin := sessionInputOrigin{}
		if d.observePromptSubmitted(sessionID, time.Now()) {
			origin = userConversationInput()
		}
		d.life.Go("maybeGenerateSessionTitleFromPrompt", func() { d.maybeGenerateSessionTitleFromPrompt(sessionID, protocol.Deref(msg.Prompt), origin) })
	}
	d.store.Touch(sessionID)
	d.sendOK(conn)
}

func (d *Daemon) persistResumeSessionID(sessionID protocol.SessionID, resumeSessionID string) {
	if _, err := d.store.TransitionSessionResumeID(sessionID, resumeSessionID); err != nil {
		d.logf("persistResumeSessionID: update failed for session %s: %v", sessionID, err)
	}
	d.rememberDispatchResume(sessionID, resumeSessionID)
}

func (d *Daemon) handleStop(conn net.Conn, msg *protocol.StopMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	reportedTranscriptPath := strings.TrimSpace(msg.TranscriptPath)
	msg.TranscriptPath = d.resolveStopTranscriptPath(d.store.Get(sessionID), reportedTranscriptPath)
	if reportedTranscriptPath != "" && msg.TranscriptPath != reportedTranscriptPath {
		d.logf("handleStop: ignored transcript path for session=%s: reported=%s bound=%s", sessionID, reportedTranscriptPath, msg.TranscriptPath)
	}
	d.logf("handleStop: session=%s, transcript_path=%s", sessionID, msg.TranscriptPath)

	relaxBackgroundWork := d.isChiefOfStaffSession(sessionID)
	classifies := !d.consumeForcedStopClassification(sessionID)
	if classifies {
		d.cancelAutoSettle(sessionID, "stop judged")
	}
	d.recordStopFacts(
		sessionID,
		!relaxBackgroundWork && hasActiveBackgroundTask(msg),
		hasPendingSessionCron(msg),
	)
	if stopIsNonTerminal(msg, relaxBackgroundWork) {
		tasks := describeBackgroundTasks(msg)
		d.logf(
			"handleStop: non-terminal stop session=%s pending_crons=%d background_tasks=[%s]",
			sessionID, protocol.Deref(msg.PendingSessionCrons), tasks,
		)
		d.traceStateEvidence(
			sessionID,
			stateOrigin{source: stateSourceStopHook, detail: "non-terminal stop: " + tasks},
			"",
		)
		d.sendOK(conn)
		if !classifies {
			d.logf("handleStop: skipping yield classification for daemon-terminated session=%s", sessionID)
			return
		}
		classification := stopClassification{
			yielded:                true,
			runningBackgroundTasks: runningBackgroundTaskCount(msg),
		}
		d.life.Go("classifyStop", func() { d.classifyStop(sessionID, msg.TranscriptPath, classification) })
		return
	}

	d.recordTurnEndedEvidence(sessionID, classifies)

	if session := d.store.Get(sessionID); session != nil {
		driver := agentdriver.Get(session.Agent)
		resumeSessionID := agentdriver.ResumeSessionIDFromTranscriptPath(driver, msg.TranscriptPath)
		observation := agentConversationObservation{SessionID: sessionID, NativeID: resumeSessionID, TranscriptPath: msg.TranscriptPath}
		switch {
		case resumeSessionID == "":
		case agentdriver.EffectiveCapabilities(driver).HasHooks:
			d.observeAgentConversation(observation)
			d.rememberDispatchResume(sessionID, resumeSessionID)
		case d.claimAgentConversation(observation):
			d.rememberDispatchResume(sessionID, resumeSessionID)
		}
	}
	d.store.Touch(sessionID)
	d.sendOK(conn)

	if !classifies {
		d.logf("handleStop: skipping classification for daemon-terminated session=%s", sessionID)
		return
	}

	d.life.Go("classifySessionState", func() { d.classifySessionState(sessionID, msg.TranscriptPath) })
	d.life.Go("maybeGenerateSessionTitle", func() { d.maybeGenerateSessionTitle(sessionID, msg.TranscriptPath) })
}

func (d *Daemon) resolveStopTranscriptPath(session *protocol.Session, reported string) string {
	if exact := d.resolveTranscriptPathForSession(session, ""); exact != "" {
		return exact
	}
	if session != nil && strings.TrimSpace(d.store.GetSessionTranscriptPath(session.ID)) != "" {
		return ""
	}
	return strings.TrimSpace(reported)
}

func (d *Daemon) resolveTranscriptPathForSession(session *protocol.Session, transcriptPath string) string {
	path := strings.TrimSpace(transcriptPath)
	if session == nil {
		return path
	}

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	if bound := strings.TrimSpace(d.store.GetSessionTranscriptPath(session.ID)); bound != "" {
		if _, err := os.Stat(bound); err == nil {
			return bound
		}
	}

	if watched := d.liveTranscriptPath(session.ID, session.Agent); watched != "" {
		return watched
	}

	return path
}

func (d *Daemon) extractLastAssistantMessage(session *protocol.Session, transcriptPath string, maxChars int, classificationStart time.Time) (string, string, error) {
	if session == nil {
		lastMessage, err := transcript.ExtractLastAssistantMessage(transcriptPath, maxChars)
		return lastMessage, "", err
	}

	driver := agentdriver.Get(session.Agent)
	lastMessage, turnID, err := agentdriver.ExtractLastAssistantForClassification(
		driver,
		transcriptPath,
		maxChars,
		classificationStart,
		d.classifiedTurnID(session.ID),
	)
	if err != nil {
		return "", "", err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID != "" && !d.beginClassifyingTurn(session.ID, turnID) {
		return "", "", agentdriver.ErrNoNewAssistantTurn
	}
	return lastMessage, turnID, nil
}

func (d *Daemon) classifiedTurnID(sessionID protocol.SessionID) string {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifiedTurn == nil {
		return ""
	}
	return d.classifiedTurn[sessionID]
}

func (d *Daemon) setClassifiedTurnID(sessionID protocol.SessionID, turnID string) {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifiedTurn == nil {
		d.classifiedTurn = make(map[protocol.SessionID]string)
	}
	d.classifiedTurn[sessionID] = turnID
}

func (d *Daemon) clearClassifiedTurn(sessionID protocol.SessionID) {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifiedTurn == nil {
		return
	}
	delete(d.classifiedTurn, sessionID)
}

func (d *Daemon) beginClassifyingTurn(sessionID protocol.SessionID, turnID string) bool {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifyingTurn == nil {
		d.classifyingTurn = make(map[protocol.SessionID]string)
	}
	if d.classifiedTurn != nil && d.classifiedTurn[sessionID] == turnID {
		return false
	}
	if d.classifyingTurn[sessionID] == turnID {
		return false
	}
	d.classifyingTurn[sessionID] = turnID
	return true
}

func (d *Daemon) clearClassifyingTurn(sessionID protocol.SessionID) {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifyingTurn == nil {
		return
	}
	delete(d.classifyingTurn, sessionID)
}

func (d *Daemon) markForcedStopClassification(sessionID protocol.SessionID) {
	if protocol.TrimID(sessionID) == "" {
		return
	}
	now := time.Now()
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	if d.forcedStop == nil {
		d.forcedStop = make(map[protocol.SessionID]time.Time)
	}
	for id, markedAt := range d.forcedStop {
		if now.Sub(markedAt) > forcedStopSuppressTTL {
			delete(d.forcedStop, id)
		}
	}
	d.forcedStop[sessionID] = now
}

func (d *Daemon) consumeForcedStopClassification(sessionID protocol.SessionID) bool {
	if protocol.TrimID(sessionID) == "" {
		return false
	}
	now := time.Now()
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	if len(d.forcedStop) == 0 {
		return false
	}
	for id, markedAt := range d.forcedStop {
		if now.Sub(markedAt) > forcedStopSuppressTTL {
			delete(d.forcedStop, id)
		}
	}
	markedAt, ok := d.forcedStop[sessionID]
	if !ok {
		return false
	}
	delete(d.forcedStop, sessionID)
	return now.Sub(markedAt) <= forcedStopSuppressTTL
}

func (d *Daemon) clearForcedStopClassification(sessionID protocol.SessionID) {
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	delete(d.forcedStop, sessionID)
}

func cloneSession(session *protocol.Session) *protocol.Session {
	if session == nil {
		return nil
	}
	clone := *session
	return &clone
}

func (d *Daemon) sessionForBroadcast(session *protocol.Session) *protocol.Session {
	decorated := d.sessionForBroadcastWithChiefOfStaff(
		session,
		d.profileChiefs(),
		d.delegatedFromChiefSessionIDs(),
		d.crewMembersBySession(),
		d.gardenDispatchSeedsBySession(),
		d.gardenDispatchersBySession(),
	)
	if decorated != nil {
		decorated.DelegationRole = d.sessionDelegationRoles()[decorated.ID]
		decorated.Automation = d.automationProvenanceForSession(decorated.ID)
		decorated.PullRequests = d.sessionPullRequestsForSession(decorated)
	}
	return decorated
}

func (d *Daemon) sessionForBroadcastWithChiefOfStaff(
	session *protocol.Session,
	chiefs map[string]protocol.SessionID,
	delegatedFromChief map[protocol.SessionID]bool,
	crewBySession map[protocol.SessionID]string,
	seedBySession map[protocol.SessionID]string,
	dispatcherBySession map[protocol.SessionID]garden.Tender,
) *protocol.Session {
	clone := cloneSession(session)
	if clone == nil {
		return nil
	}
	if d.store != nil {
		clone.TerminalExit = d.store.GetSessionExit(clone.ID)
	}
	d.decorateSessionWithStateReason(clone)

	d.decorateSessionWithAutoSettle(clone)
	d.decorateSessionWithSnooze(clone)
	d.decorateChiefOfStaff(clone, chiefs)
	d.decorateDelegatedFromChief(clone, delegatedFromChief)
	d.decorateCrewMember(clone, crewBySession)
	d.decorateSessionSeed(clone, seedBySession)
	d.decorateSessionDispatcher(clone, dispatcherBySession)
	if seedBySession[clone.ID] != "" && clone.SeedID == nil {
		clone.DispatcherMember, clone.DispatcherSessionID = nil, nil
		clone.DelegatedFromChief = nil
	}
	d.decorateSessionWithCost(clone)
	d.decorateSessionWithTerminalBuild(clone)
	d.decorateSessionWithTurn(clone)
	return clone
}

func (d *Daemon) sessionsForBroadcast(sessions []*protocol.Session) []protocol.Session {
	if len(sessions) == 0 {
		return nil
	}
	chiefs := d.profileChiefs()
	delegatedFromChief := d.delegatedFromChiefSessionIDs()
	crewBySession := d.crewMembersBySession()
	seedBySession := d.gardenDispatchSeedsBySession()
	dispatcherBySession := d.gardenDispatchersBySession()
	rolesBySession := d.sessionDelegationRoles()
	bySession := d.latestAutomationProvenance()
	pullRequestsBySession := d.store.ListSessionPullRequestsBySession()
	pullRequestWatchesByPR := d.pullRequestWatchesByPR()
	out := make([]protocol.Session, 0, len(sessions))
	for _, session := range sessions {
		if decorated := d.sessionForBroadcastWithChiefOfStaff(session, chiefs, delegatedFromChief, crewBySession, seedBySession, dispatcherBySession); decorated != nil {
			decorated.DelegationRole = rolesBySession[decorated.ID]
			decorated.Automation = bySession[decorated.ID]
			addresses := []inbox.Address{inbox.ToSession(decorated.ID)}
			if member := crewBySession[decorated.ID]; member != "" {
				addresses = append(addresses, inbox.ToMember(member))
			}
			if chiefs[decorated.ProfileID] == decorated.ID {
				addresses = append(addresses, inbox.ToChief(decorated.ProfileID))
			}
			decorated.PullRequests = d.sessionPullRequestsForBroadcast(d.sessionPullRequestRecords(decorated.ID, addresses, pullRequestsBySession, pullRequestWatchesByPR), addresses, pullRequestWatchesByPR)
			out = append(out, *decorated)
		}
	}
	return out
}

func (d *Daemon) sessionDelegationRoles() map[protocol.SessionID]*protocol.SessionDelegationRole {
	roles, err := d.store.SessionDelegationRoles()
	if err != nil {
		d.logf("session delegation roles: %v", err)
	}
	return roles
}

func (d *Daemon) mergedSessionsForBroadcast() []protocol.Session {
	localSessions := d.sessionsForBroadcast(d.store.List(""))
	remoteSessions := d.remoteSessionsForBroadcast()
	if len(localSessions) == 0 {
		return remoteSessions
	}
	if len(remoteSessions) == 0 {
		return localSessions
	}
	merged := make([]protocol.Session, 0, len(localSessions)+len(remoteSessions))
	merged = append(merged, localSessions...)
	merged = append(merged, remoteSessions...)
	return merged
}

func (d *Daemon) remoteSessionsForBroadcast() []protocol.Session {
	if d.hubManager == nil {
		return nil
	}
	sessions := d.hubManager.RemoteSessions()
	chiefs := d.profileChiefs()
	for i := range sessions {
		d.decorateChiefOfStaff(&sessions[i], chiefs)
	}
	return sessions
}

func (d *Daemon) broadcastSessionStateChanged(sessionID protocol.SessionID) {
	d.publishFact(FactSessionStateChanged, string(sessionID), nil)
}

func (d *Daemon) projectSessionStateChanged(sessionID protocol.SessionID) {
	session := d.store.Get(sessionID)
	decorated := d.sessionForBroadcast(session)
	if decorated == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionStateChanged,
		Session: decorated,
	})
}

type rateLimitWindow struct {
	ResetAt string `json:"reset_at"`
}

func (d *Daemon) broadcastRateLimited(resource string, resetAt time.Time) {
	d.publishFact(FactRateLimited, resource, rateLimitWindow{
		ResetAt: string(protocol.NewTimestamp(resetAt)),
	})
}

func (d *Daemon) projectRateLimited(ev bus.Event) {
	window, ok := decodeFact[rateLimitWindow](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:             protocol.EventRateLimited,
		RateLimitResource: protocol.Ptr(ev.Subject),
		RateLimitResetAt:  protocol.Ptr(window.ResetAt),
	})
}

func (d *Daemon) handleFilesEdited(conn net.Conn, msg *protocol.FilesEditedMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	for _, path := range msg.Paths {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".markdown":
		default:
			continue
		}
		d.store.RecordFileActivity(path, store.FileActivitySourceEdited, sessionID)
	}
	d.sendOK(conn)
}

func (d *Daemon) handleQuery(conn net.Conn, msg *protocol.QueryMessage) {
	sessions := d.store.List(protocol.Deref(msg.Filter))
	profiles, err := d.liveProtocolProfiles()
	if err != nil {
		d.sendError(conn, "query: "+err.Error())
		return
	}
	resp := protocol.Response{
		Ok:       true,
		Sessions: d.sessionsForBroadcast(sessions),
		Profiles: profiles,
	}
	if msg.CallerID != nil {
		resp.CallerSessionID = protocol.Ptr(d.sessionInTerminal(*msg.CallerID))
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleHeartbeat(conn net.Conn, msg *protocol.HeartbeatMessage) {
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleQueryPRs(conn net.Conn, msg *protocol.QueryPRsMessage) {
	prs := d.store.ListPRs(protocol.Deref(msg.Filter))
	resp := protocol.Response{
		Ok:  true,
		Prs: protocol.PRsToValues(prs),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleMutePR(conn net.Conn, msg *protocol.MutePRMessage) {
	d.store.ToggleMutePR(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleMuteRepo(conn net.Conn, msg *protocol.MuteRepoMessage) {
	d.store.ToggleMuteRepo(msg.Repo)
	d.sendOK(conn)
}

func (d *Daemon) handleCollapseRepo(conn net.Conn, msg *protocol.CollapseRepoMessage) {
	d.store.SetRepoCollapsed(msg.Repo, msg.Collapsed)
	d.sendOK(conn)
}

func (d *Daemon) handleQueryRepos(conn net.Conn, msg *protocol.QueryReposMessage) {
	repos := d.store.ListRepoStates()
	resp := protocol.Response{
		Ok:    true,
		Repos: protocol.RepoStatesToValues(repos),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleQueryAuthors(conn net.Conn, msg *protocol.QueryAuthorsMessage) {
	authors := d.store.ListAuthorStates()
	resp := protocol.Response{
		Ok:      true,
		Authors: protocol.AuthorStatesToValues(authors),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) fetchPRDetailsForID(id string) ([]*protocol.PR, error) {
	if !d.githubAvailable() {
		return nil, fmt.Errorf("GitHub client not available")
	}

	host, repo, _, err := protocol.ParsePRID(id)
	if err != nil {
		return nil, err
	}

	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, fmt.Errorf("no client for host %s", host)
	}

	prs := d.store.ListPRsByRepoHost(repo, host)

	for _, pr := range prs {
		if d.stopping() {
			break
		}
		if pr.NeedsDetailRefresh() {
			details, err := client.FetchPRDetails(pr.Repo, pr.Number)
			if err != nil {
				d.logf("Failed to fetch details for %s: %v", pr.ID, err)
				continue
			}
			d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		}
	}

	updatedPRs := d.store.ListPRsByRepoHost(repo, host)
	return updatedPRs, nil
}

func (d *Daemon) handleFetchPRDetails(conn net.Conn, msg *protocol.FetchPRDetailsMessage) {
	updatedPRs, err := d.fetchPRDetailsForID(msg.ID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	resp := protocol.Response{
		Ok:  true,
		Prs: protocol.PRsToValues(updatedPRs),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendOK(conn net.Conn) {
	resp := protocol.Response{Ok: true}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendError(conn net.Conn, errMsg string) {
	resp := protocol.Response{Ok: false, Error: protocol.Ptr(errMsg)}
	json.NewEncoder(conn).Encode(resp)
}

type successfulPRObservation struct {
	prs        []*protocol.PR
	observedAt time.Time
}

func (d *Daemon) pollPRs() {
	if !d.githubAvailable() {
		d.log("GitHub client not available, PR polling disabled")
		return
	}

	d.log("PR polling started (90s interval)")

	d.doPRPoll()

	ticker := time.NewTicker(90 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.life.Done():
			return
		case <-ticker.C:
			d.doPRPoll()
		}
	}
}

func (d *Daemon) doPRPoll() {
	if !d.githubAvailable() {
		return
	}
	d.prRefreshMu.Lock()

	var allPRs []*protocol.PR
	observedByHost := make(map[string]successfulPRObservation)
	skippedHosts := make(map[string]bool)
	var earliestReset time.Time

	for _, host := range d.ghRegistry.Hosts() {
		if d.stopping() {
			break
		}
		client, ok := d.ghRegistry.Get(host)
		if !ok {
			continue
		}

		if limited, resetAt := client.IsRateLimited("search"); limited {
			d.logf("PR poll skipped for %s: search API rate limited until %s", host, resetAt.Format(time.RFC3339))
			skippedHosts[host] = true
			if earliestReset.IsZero() || resetAt.Before(earliestReset) {
				earliestReset = resetAt
			}
			continue
		}

		observedAt := time.Now()
		prs, err := client.FetchAll()
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("search"); info != nil {
					d.logf("PR poll rate limited for %s until %s", host, info.ResetAt.Format(time.RFC3339))
					if earliestReset.IsZero() || info.ResetAt.Before(earliestReset) {
						earliestReset = info.ResetAt
					}
				} else {
					d.logf("PR poll rate limited for %s (unknown reset time)", host)
					resetAt := time.Now().Add(60 * time.Second)
					if earliestReset.IsZero() || resetAt.Before(earliestReset) {
						earliestReset = resetAt
					}
				}
				skippedHosts[host] = true
				continue
			}
			if errors.Is(err, github.ErrSelfRateLimited) {
				d.logf("PR poll: self-rate-limited for %s, skipping", host)
				skippedHosts[host] = true
				continue
			}
			d.logf("PR poll error for %s: %v", host, err)
			skippedHosts[host] = true
			continue
		}

		allPRs = append(allPRs, prs...)
		observedByHost[host] = successfulPRObservation{prs: prs, observedAt: observedAt}
	}

	if !earliestReset.IsZero() {
		d.broadcastRateLimited("search", earliestReset)
	}

	stored := d.life.Do("doPRPoll", func() { d.storePRPoll(allPRs, skippedHosts, observedByHost) })
	d.prRefreshMu.Unlock()
	if stored {
		d.doDetailRefresh()
	}
}

func (d *Daemon) storePRPoll(allPRs []*protocol.PR, skippedHosts map[string]bool, observedByHost map[string]successfulPRObservation) {
	if len(skippedHosts) > 0 {
		existing := d.store.ListPRs("")
		for _, pr := range existing {
			host := pr.Host
			if host == "" {
				if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
					host = parsedHost
				}
			}
			if host != "" && skippedHosts[host] {
				allPRs = append(allPRs, pr)
			}
		}
	}

	previousPRs := d.store.ListPRs("")
	d.store.SetPRs(allPRs)

	currentPRs := d.store.ListPRs("")
	d.publishPRSetChanges(previousPRs, currentPRs)

	waiting := 0
	for _, pr := range currentPRs {
		if pr.State == protocol.PRStateWaiting && !pr.Muted {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(currentPRs), waiting)

	for host, observation := range observedByHost {
		d.observeGitHubReviewRequests(host, observation.prs, observation.observedAt)
	}
}

func (d *Daemon) doDetailRefresh() {
	if !d.githubAvailable() {
		return
	}

	if !d.life.Do("doDetailRefresh", d.store.DecayHeatStates) {
		return
	}

	prs := d.store.GetPRsNeedingDetailRefresh()
	if len(prs) == 0 {
		return
	}

	d.logf("Detail refresh: %d PRs need refresh", len(prs))

	var refreshedIDs []string
	limitedHosts := make(map[string]time.Time)
	for _, pr := range prs {
		if d.stopping() {
			break
		}
		host := pr.Host
		if host == "" {
			if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
				host = parsedHost
			}
		}
		if host == "" {
			continue
		}
		if _, limited := limitedHosts[host]; limited {
			continue
		}

		client, ok := d.ghRegistry.Get(host)
		if !ok {
			d.logf("Detail refresh: no client for host %s", host)
			continue
		}

		if limited, resetAt := client.IsRateLimited("core"); limited {
			d.logf("Detail refresh: %s rate limited until %v", host, resetAt)
			limitedHosts[host] = resetAt
			continue
		}

		details, err := client.FetchPRDetails(pr.Repo, pr.Number)
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("core"); info != nil {
					d.logf("Detail refresh: %s rate limited, stopping host refresh", host)
					limitedHosts[host] = info.ResetAt
				}
				continue
			}
			if errors.Is(err, github.ErrSelfRateLimited) {
				d.logf("Detail refresh: %s self-rate-limited, stopping host refresh", host)
				limitedHosts[host] = time.Now().Add(60 * time.Second)
				continue
			}
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}

		stored := d.life.Do("doDetailRefresh", func() {
			if prHeadSHA := protocol.Deref(pr.HeadSHA); prHeadSHA != "" && details.HeadSHA != prHeadSHA {
				d.store.SetPRHot(pr.ID)
			}
			d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		})
		if !stored {
			return
		}
		refreshedIDs = append(refreshedIDs, pr.ID)
	}

	if len(limitedHosts) > 0 {
		var earliest time.Time
		for _, resetAt := range limitedHosts {
			if resetAt.IsZero() {
				continue
			}
			if earliest.IsZero() || resetAt.Before(earliest) {
				earliest = resetAt
			}
		}
		if !earliest.IsZero() {
			d.broadcastRateLimited("core", earliest)
		}
	}

	d.life.Do("doDetailRefresh", func() { d.publishPRDetailsChanged("Detail refresh", refreshedIDs) })
}

func (d *Daemon) publishPRDetailsChanged(origin string, ids []string) {
	if len(ids) == 0 {
		return
	}
	d.logf("%s: updated %d PRs", origin, len(ids))
	d.coalesceSnapshots(func() {
		for _, id := range ids {
			d.publishFact(FactPRDetailsChanged, id, nil)
		}
	})
}

func (d *Daemon) fetchAllPRDetails() {
	if !d.githubAvailable() {
		return
	}

	allPRs := d.store.ListPRs("")
	if len(allPRs) == 0 {
		return
	}

	d.logf("App launch: fetching details for %d PRs", len(allPRs))

	var refreshedIDs []string
	limitedHosts := make(map[string]time.Time)
	for _, pr := range allPRs {
		if d.stopping() {
			break
		}
		if pr.Muted {
			continue
		}
		repoState := d.store.GetRepoState(pr.Repo)
		if repoState != nil && repoState.Muted {
			continue
		}

		host := pr.Host
		if host == "" {
			if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
				host = parsedHost
			}
		}
		if host == "" {
			continue
		}
		if _, limited := limitedHosts[host]; limited {
			continue
		}

		client, ok := d.ghRegistry.Get(host)
		if !ok {
			d.logf("App launch: no client for host %s", host)
			continue
		}

		if limited, resetAt := client.IsRateLimited("core"); limited {
			d.logf("App launch: %s rate limited until %v", host, resetAt)
			limitedHosts[host] = resetAt
			continue
		}

		details, err := client.FetchPRDetails(pr.Repo, pr.Number)
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("core"); info != nil {
					d.logf("App launch: %s rate limited, stopping host fetch loop", host)
					limitedHosts[host] = info.ResetAt
				}
				continue
			}
			if errors.Is(err, github.ErrSelfRateLimited) {
				d.logf("App launch: %s self-rate-limited, stopping host fetch loop", host)
				limitedHosts[host] = time.Now().Add(60 * time.Second)
				continue
			}
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}

		if !d.life.Do("fetchAllPRDetails", func() {
			d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		}) {
			return
		}
		refreshedIDs = append(refreshedIDs, pr.ID)
	}

	if len(limitedHosts) > 0 {
		var earliest time.Time
		for _, resetAt := range limitedHosts {
			if resetAt.IsZero() {
				continue
			}
			if earliest.IsZero() || resetAt.Before(earliest) {
				earliest = resetAt
			}
		}
		if !earliest.IsZero() {
			d.broadcastRateLimited("core", earliest)
		}
	}

	d.life.Do("fetchAllPRDetails", func() { d.publishPRDetailsChanged("App launch", refreshedIDs) })
}

func (d *Daemon) handleInjectTestPR(conn net.Conn, msg *protocol.InjectTestPRMessage) {
	if msg.PR.ID == "" {
		d.sendError(conn, "PR ID cannot be empty")
		return
	}

	existing := d.store.GetPR(msg.PR.ID)
	d.store.AddPR(&msg.PR)
	d.sendOK(conn)

	if existing == nil {
		d.publishFact(FactPRAppeared, msg.PR.ID, nil)
	} else {
		d.publishFact(FactPRUpdated, msg.PR.ID, nil)
	}
}

func (d *Daemon) handleInjectTestSession(conn net.Conn, msg *protocol.InjectTestSessionMessage) {
	if msg.Session.ID == "" {
		d.sendError(conn, "Session ID cannot be empty")
		return
	}

	msg.Session.Agent = normalizeStoredSessionAgent(msg.Session.Agent, protocol.SessionAgentCodex)
	stampSessionTimestamps(&msg.Session, string(protocol.TimestampNow()))
	profile, err := d.requestedOrRecentProfile(msg.Session.ProfileID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	msg.Session.ProfileID = profile.ID
	if member := strings.TrimSpace(protocol.Deref(msg.Session.CrewMember)); member != "" {
		if _, err := d.claimCrewBinding(member, msg.Session.ID); err != nil {
			d.sendError(conn, fmt.Sprintf("crew bind %q: %v", member, err))
			return
		}
	}
	if err := d.store.AddChecked(&msg.Session); err != nil {
		d.releaseCrewBindingIfSession(msg.Session.ID)
		d.sendError(conn, err.Error())
		return
	}
	d.publishFact(FactSessionRegistered, string(msg.Session.ID), nil)
	if !protocol.Deref(msg.Unplaced) {
		d.placeLaunchedSession(&msg.Session, &launchPlacement{direction: layouttree.DirectionVertical, focus: true})
	}
	d.sendOK(conn)
}

func (d *Daemon) RefreshPRs() {
	if !d.githubAvailable() {
		return
	}
	d.doPRPoll()
}

func (d *Daemon) doRefreshPRsWithResult() error {
	if !d.githubAvailable() {
		return fmt.Errorf("GitHub client not available")
	}
	d.prRefreshMu.Lock()
	defer d.prRefreshMu.Unlock()

	var allPRs []*protocol.PR
	observedByHost := make(map[string]successfulPRObservation)
	skippedHosts := make(map[string]bool)
	var firstErr error
	successCount := 0

	for _, host := range d.ghRegistry.Hosts() {
		if d.stopping() {
			break
		}
		client, ok := d.ghRegistry.Get(host)
		if !ok {
			continue
		}
		observedAt := time.Now()
		prs, err := client.FetchAll()
		if err != nil {
			d.logf("PR refresh error for %s: %v", host, err)
			if firstErr == nil {
				firstErr = err
			}
			skippedHosts[host] = true
			continue
		}
		successCount++
		allPRs = append(allPRs, prs...)
		observedByHost[host] = successfulPRObservation{prs: prs, observedAt: observedAt}
	}

	if len(skippedHosts) > 0 {
		existing := d.store.ListPRs("")
		for _, pr := range existing {
			host := pr.Host
			if host == "" {
				if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
					host = parsedHost
				}
			}
			if host != "" && skippedHosts[host] {
				allPRs = append(allPRs, pr)
			}
		}
	}

	// The host loop stops early once stopping, so saving its set would drop the hosts it skipped.
	var currentPRs []*protocol.PR
	if !d.life.Do("doRefreshPRs", func() {
		previousPRs := d.store.ListPRs("")
		d.store.SetPRs(allPRs)
		currentPRs = d.store.ListPRs("")
		d.publishPRSetChanges(previousPRs, currentPRs)
	}) {
		return errDaemonStopping
	}

	d.logf("PR refresh: %d PRs fetched", len(currentPRs))
	for host, observation := range observedByHost {
		d.observeGitHubReviewRequests(host, observation.prs, observation.observedAt)
	}
	if successCount == 0 && firstErr != nil {
		return fmt.Errorf("failed to fetch PRs: %w", firstErr)
	}
	return nil
}

func (d *Daemon) fetchPRDetailsImmediate(prID string) {
	if !d.githubAvailable() {
		return
	}

	pr := d.store.GetPR(prID)
	if pr == nil {
		return
	}

	if pr.Muted {
		return
	}
	repoState := d.store.GetRepoState(pr.Repo)
	if repoState != nil && repoState.Muted {
		return
	}

	host := pr.Host
	if host == "" {
		if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
			host = parsedHost
		}
	}
	if host == "" {
		return
	}

	client, ok := d.ghRegistry.Get(host)
	if !ok {
		d.logf("Immediate fetch: no client for host %s", host)
		return
	}

	if limited, resetAt := client.IsRateLimited("core"); limited {
		d.logf("Immediate fetch skipped for %s: rate limited until %v", prID, resetAt)
		return
	}

	d.store.SetPRHot(prID)

	details, err := client.FetchPRDetails(pr.Repo, pr.Number)
	if err != nil {
		if errors.Is(err, github.ErrRateLimited) {
			d.logf("Immediate fetch for %s: rate limited", prID)
			if info := client.GetRateLimit("core"); info != nil {
				d.broadcastRateLimited("core", info.ResetAt)
			}
			return
		}
		if errors.Is(err, github.ErrSelfRateLimited) {
			d.logf("Immediate fetch for %s: self-rate-limited", prID)
			return
		}
		d.logf("Immediate fetch failed for %s: %v", prID, err)
		return
	}

	d.store.UpdatePRDetails(prID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
	d.logf("Immediate fetch complete for %s (heat=hot)", prID)
}

func (d *Daemon) monitorBranches() {
	d.logf("Branch monitoring started (%s interval)", branchMonitorInterval)

	ticker := time.NewTicker(branchMonitorInterval)
	defer ticker.Stop()

	for d.life.Do("monitorBranches", d.checkAllBranches) {
		select {
		case <-d.life.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) checkAllBranches() {
	sessions := d.store.List("")

	d.coalesceSnapshots(func() {
		for _, session := range sessions {
			info, err := d.readBranchInfo(context.Background(), gitTask{Kind: gitTaskSessionIdentity, Lane: gitDeferred}, session.Directory)
			if err != nil {
				continue
			}

			if info.Branch != protocol.Deref(session.Branch) || info.IsWorktree != protocol.Deref(session.IsWorktree) ||
				info.Repository != protocol.Deref(session.Repository) {
				d.store.UpdateBranch(session.ID, info.Branch, info.IsWorktree, info.MainRepo, info.Repository)
				d.logf("Branch changed: session=%s branch=%s isWorktree=%v", session.ID, info.Branch, info.IsWorktree)
				d.publishFact(FactSessionBranchChanged, string(session.ID), nil)
			}
		}
	})
}

func (d *Daemon) publishSessionsReconciled(report workerReconcileReport) {
	if !report.Changed {
		return
	}
	d.coalesceSnapshots(func() {
		for _, sessionID := range report.ChangedSessionIDs {
			d.publishFact(FactSessionReconciled, sessionID, nil)
		}
	})
}

func (d *Daemon) publishEndpointSessionsChanged(endpointID string) {
	d.publishFact(FactEndpointSessionsChanged, endpointID, nil)
}

func (d *Daemon) projectSessionsUpdated() {
	d.projectSnapshot(snapshotSessions, func() {
		if d.wsHub == nil || d.store == nil {
			return
		}
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: d.mergedSessionsForBroadcast(),
		})
	})
}

func (d *Daemon) listEndpointInfos() []protocol.EndpointInfo {
	if d.hubManager == nil {
		records := d.store.ListEndpoints()
		out := make([]protocol.EndpointInfo, 0, len(records))
		for _, record := range records {
			out = append(out, protocol.EndpointInfo{
				ID:        record.ID,
				Name:      record.Name,
				SshTarget: record.SSHTarget,
				Status:    "disconnected",
				Enabled:   protocol.Ptr(record.Enabled),
			})
		}
		return out
	}
	return d.hubManager.List()
}

func (d *Daemon) broadcastEndpointStatusChanged(info protocol.EndpointInfo) {
	d.publishFact(FactEndpointStatusChanged, info.ID, info)
}

func (d *Daemon) projectEndpointStatusChanged(ev bus.Event) {
	info, ok := decodeFact[protocol.EndpointInfo](d, ev)
	if !ok {
		return
	}
	d.broadcastMessage(&protocol.EndpointStatusChangedMessage{
		Event:    protocol.EventEndpointStatusChanged,
		Endpoint: info,
	})
}

func (d *Daemon) projectEndpointsUpdated() {
	d.projectSnapshot(snapshotEndpoints, func() {
		d.broadcastMessage(&protocol.EndpointsUpdatedMessage{
			Event:     protocol.EventEndpointsUpdated,
			Endpoints: d.listEndpointInfos(),
		})
	})
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	sessions := d.store.List("")
	prs := d.store.ListPRs("")
	dataDir, socketPath, routingPathError := healthRoutingPaths()
	status := "starting"
	select {
	case <-d.Started():
		status = "ok"
	default:
	}

	health := map[string]interface{}{
		"status":             status,
		"version":            buildinfo.Version,
		"build_time":         buildinfo.BuildTime,
		"protocol":           protocol.ProtocolVersion,
		"source_fingerprint": buildinfo.SourceFingerprint,
		"git_commit":         buildinfo.GitCommit,
		"daemon_instance_id": d.daemonInstanceID,
		"sessions":           len(sessions),
		"prs":                len(prs),
		"ws_clients":         d.wsHub.ClientCount(),
		"github_available":   d.githubAvailable(),
		"github_polling_off": gitHubPollingOffReason(),
		"instance":           config.InstanceLabel(),
		"data_dir":           dataDir,
		"socket_path":        socketPath,
		"port":               config.WSPort(),
		"headless_tasks":     headless.Describe(),
	}
	if routingPathError != "" {
		health["routing_path_error"] = routingPathError
	}
	if status, err := d.enrollmentStatus(); err != nil {
		health["enrollment"] = "unknown"
		health["enrollment_error"] = err.Error()
	} else {
		health["enrollment"] = status.Describe()
		health["home_daemon_id"] = status.HomeDaemonID
	}

	setNoStoreHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func healthRoutingPaths() (dataDir, socketPath, routingPathError string) {
	rawDataDir := config.DataDir()
	rawSocketPath := config.SocketPath()
	dataDir, dataErr := config.CanonicalRuntimePath(rawDataDir)
	if dataErr != nil {
		dataDir = rawDataDir
	}
	socketPath, socketErr := config.CanonicalRuntimePath(rawSocketPath)
	if socketErr != nil {
		socketPath = rawSocketPath
	}
	if dataErr != nil || socketErr != nil {
		routingPathError = fmt.Sprintf("data_dir: %v; socket_path: %v", dataErr, socketErr)
	}
	return dataDir, socketPath, routingPathError
}
