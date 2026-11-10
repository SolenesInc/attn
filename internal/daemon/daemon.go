package daemon

import (
	"bufio"
	"context"
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/singleflight"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/diag"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
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
	warnGHNotInstalled            = "gh_not_installed"
	warnGHVersionTooOld           = "gh_version_too_old"
)

var ErrAlreadyRunning = errors.New("daemon already running")

const (
	PIDLockReleaseTimer stepclock.Name = "pid-lock-release"
	// A gone daemon's child keeps the lock only until its exec closes the inherited descriptor:
	// at most 38ms over 16 such holds, on 4 CPUs shared with 12 busy loops (2026-11-05).
	PIDLockReleaseTripwire = 5 * time.Second
)

type Daemon struct {
	monitorsOnce        sync.Once
	monitorState        *monitorIndex
	outposts            outpostsRuntime
	releases            releaseAvailability
	homeUpdate          homeUpdateNotice
	updateRuns          updateRuns
	outpost             outpostRuntime
	homeGateway         homeGatewayConns
	phones              phonesRuntime
	senders             senderPairings
	recentFileStat      func(string) (os.FileInfo, error)
	recentFilePruneGate chan struct{}
	socketPath          string
	pidPath             string
	run                 daemonRun
	pidFile             *os.File
	dataRoot            string
	daemonInstanceID    string
	clientToken         string
	store               *store.Store
	gardenSeeds         store.DocumentsMemo[gardenSeeds]
	// Serializes PR fetches with review-request edge reconciliation.
	prRefreshMu             sync.Mutex
	launchFetch             launchFetch
	prDetailFetches         prDetailFetches
	automationMu            sync.Mutex
	automationLaunchResults sync.Map
	automationLaunchWaits   sync.Map
	automationRepoMu        sync.Mutex
	automationRepos         map[string]*sync.Mutex
	wsWriteTimeout          time.Duration
	wsPingInterval          time.Duration
	wsPingTimeout           time.Duration
	listener                net.Listener
	httpServer              *http.Server
	httpListener            net.Listener
	httpHandler             http.Handler
	diagServer              *diag.Server
	harnessWSListenerFD     string
	wsHub                   *wsHub
	startedAt               time.Time
	analyticsUpload         analyticsUpload
	analyticsOnSince        atomic.Pointer[time.Time]
	appIdentity             atomic.Pointer[appIdentity]
	lastPresentAt           time.Time
	presenceMu              sync.RWMutex
	crewLifecycleState      *crewLifecycleMemo
	crewMemoOnce            sync.Once
	crewCharterMu           sync.Mutex
	life                    lifetime
	stopOnce                sync.Once
	logger                  *logging.Logger
	debugLogging            bool
	ghRegistry              *github.ClientRegistry
	reopenGitMu             sync.Mutex
	reopenBranches          *sharedCalls[reopenBranchKey, branchInspection]
	reopenInspect           func(context.Context, *git.Client, string, string) (branchInspection, error)
	gitExec                 gitExecutor
	worktreeMaintenance     worktreeMaintenanceCoordinator
	warnings                []protocol.DaemonWarning
	warningsMu              sync.RWMutex
	ptyBackend              ptybackend.Backend
	ptySettingsMu           sync.Mutex
	ptySettingsChangeMu     sync.Mutex
	sharedPTYHost           *ptybackend.WorkerBackend
	upgradingMu             sync.Mutex
	upgradingWorkers        map[harness.TerminalID]chan struct{}
	ptyRecovered            chan struct{}
	monitorTerminalsDirty   atomic.Bool

	sessionTitleExec func(ctx context.Context, session *protocol.Session, conversation string) (string, error)

	seedArtifactMu              sync.Mutex
	harnessModelQueries         singleflight.Group
	harnessModelCatalogs        sync.Map
	delegationMu                sync.Mutex
	delegationRunning           map[string]bool
	delegationCheckoutMu        sync.Mutex
	delegationWaitsForFirstTurn bool
	launchWatchMu               sync.Mutex
	codexTrustMu                sync.Mutex
	launchWatches               map[protocol.SessionID]*launchWatch
	recoveredLaunches           map[protocol.SessionID]*launchWatch
	typedCommands               sync.Map
	terminalExitIntentMu        sync.Mutex
	terminalExitIntents         map[harness.TerminalID]terminalExitIntent
	replacedRuntimes            map[harness.TerminalID][]string

	teardownMu            sync.Mutex
	tearingDown           map[protocol.SessionID]chan struct{}
	sessionLifecycleLocks sessionLocks
	terminalEndLocks      sessionLocks
	spawnLocksMu          sync.Mutex
	spawnLocks            map[protocol.SessionID]*spawnLock

	terminalsOnce            sync.Once
	ptySizes                 terminalSizes
	routerOnce               sync.Once
	routes                   *Router
	terminalState            *terminalRegistry
	noAgentSockets           bool
	claudeModOnce            sync.Once
	tenderResumes            tenderResumeLedger
	compactions              compactionLedger
	claudeModState           *claudeMod
	codexSharedOnce          sync.Once
	codexSharedState         *codexShared
	nisseOnce                sync.Once
	nisseState               *nisseLink
	quickCaptureAssetMu      sync.Mutex
	inboxMu                  sync.Mutex
	inboxStates              map[who.Address]*inboxDeliveryState
	inboxUnsubscribe         func()
	monitorPolicyUnsubscribe func()
	crewStartMu              sync.Mutex
	crewExitedMu             sync.Mutex
	crewExitedSessions       map[string]string
	supportInputTraceOnce    sync.Once
	supportInputTrace        *supportInputTraceRing

	emptyDesktops emptyDesktopRemoval

	dictationRefinements dictationRefinements

	recoveryMu          sync.RWMutex
	recovering          bool
	recoverySettled     chan struct{}
	notebookSync        notebookSyncRuntime
	notebookSyncWatch   bool
	notebookMu          sync.Mutex
	notebookStores      map[string]*notebook.Store
	rootWatcherMu       sync.Mutex
	rootWatch           func(*stepclock.Clock, string, func([]string)) (*notebook.Watcher, error)
	clock               *stepclock.Clock
	rootWatchers        map[string]*notebook.Watcher
	fsMu                sync.Mutex
	fsStores            map[string]*fsdoc.Store
	fsWatchMu           sync.Mutex
	fsWatchRefs         map[string]map[*wsClient]int
	pendingInitialWS    map[*wsClient]struct{}
	gitHubHostsKnown    chan struct{}
	startedOnce         sync.Once
	startedCh           chan struct{}
	plugins             *pluginRegistry
	pluginSupervisorMu  sync.Mutex
	pluginSupervisor    *pluginSupervisor
	pluginHealthEnabled bool

	pluginDir      string
	busPinMu       sync.Mutex
	busPinEpisodes map[string]*busPinEpisode
	busPinAge      time.Duration
	removePlugin   func(pluginDir, name string) error
	pluginActionMu sync.Mutex

	worktreePluginCallTimeout         time.Duration
	worktreeCreateProviderCallTimeout time.Duration

	loginEnv         *loginEnv
	pluginStartEnded chan struct{}

	terminalThemeMu sync.Mutex
	terminalTheme   pty.TerminalTheme

	openTileMu sync.Mutex

	desktopTiles  desktopTileDelivery
	tileDocuments tileDocumentWatch

	browserControl  appRequests[browserControlResult]
	calloutRequests appRequests[calloutReply]

	lastBackupMu sync.Mutex
	lastBackupAt time.Time

	gardenMintNoteID       func() (string, error)
	gardenNow              func() time.Time
	gitHubPollingOffLogged bool
	checkIDsMu             sync.Mutex
	gardenWatchMu          sync.Mutex
	// gardenBellsResolvedThrough is the last bus seq whose seed bells were resolved; gardenWatchMu guards it.
	gardenBellsResolvedThrough int64
	gardenReviewMu             sync.Mutex
	dispatchSeedsMu            sync.Mutex
	dispatchSeeds              map[protocol.SessionID]string
	dispatchersBySession       map[protocol.SessionID]who.Actor
	dispatchFromChief          map[protocol.SessionID]bool
	dispatchProjectionRevs     map[protocol.SessionID]int64
	dispatchSeedsLoaded        bool

	automationsBroadcastHook func(*protocol.AutomationsChangedMessage)

	eventBus                       *bus.Bus
	busUnsubscribe                 func()
	gardenSeedEventConsumerErr     error
	gardenSeedEventConsumerStarted bool

	docSubsMu           sync.Mutex
	docSubs             map[string]*docSubscription
	docSubsSeq          int64
	docUnsubHooks       func()
	sessionPRUnsubHooks func()
	sessionPRHosts      func(host string) (sessionPRHost, bool)
	sessionPRRefreshMu  sync.Mutex
	beforeSeedMoveWrite func(seedID string)

	harvestWhenMu        sync.Mutex
	harvestWhenUntracked map[string]bool

	snapshotMu           sync.Mutex
	snapshotDepth        int
	pendingSnapshots     map[string]func()
	pendingSnapshotOrder []string

	conversationKeepMu   sync.Mutex
	jobQueueMu           sync.RWMutex
	jobQueue             *jobs.Runner
	jobQueueRoleChanged  chan struct{}
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

	d := &Daemon{
		recentFileStat:      os.Stat,
		notebookSyncWatch:   true,
		rootWatch:           openRootWatch,
		clock:               stepclock.Process(),
		recentFilePruneGate: make(chan struct{}, 1),
		ptyRecovered:        make(chan struct{}),
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		wsHub:               newWSHub(),
		startedAt:           time.Now(),
		desktopTiles:        newDesktopTileDelivery(),
		tileDocuments:       newTileDocumentWatch(),
		logger:              logger,
		debugLogging:        logger != nil && logger.DebugEnabled(),
		ghRegistry:          github.NewClientRegistry(),

		pendingInitialWS: make(map[*wsClient]struct{}),
		gitHubHostsKnown: make(chan struct{}),
		startedCh:        make(chan struct{}),

		plugins:             newPluginRegistry(),
		pluginHealthEnabled: true,
		jobQueueRoleChanged: make(chan struct{}, 1),
		pluginDir:           pluginDirForSocket(socketPath),
		spawnLocks:          make(map[protocol.SessionID]*spawnLock),
	}
	d.wireGitExecution(productionGitExecutorConfig)
	d.delegationWaitsForFirstTurn = true
	d.sessionTitleExec = d.execSessionTitle
	d.loginEnv = newLoginEnv(&d.life, d.logf)
	d.pluginStartEnded = make(chan struct{})
	return d
}

func (d *Daemon) startPTYBackend() error {
	selected := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_PTY_BACKEND")))
	switch selected {
	case "worker":
		return d.startDedicatedWorkers()
	case "shared":
		shared, err := d.newSharedPTYHost()
		if err != nil {
			return fmt.Errorf("start the shared PTY host: %w", err)
		}
		d.ptyBackend = shared
		d.sharedPTYHost = shared
		d.logf("using PTY backend: shared")
		return nil
	case "", "migrating":
		return d.startMigratingPTYBackend()
	default:
		return fmt.Errorf("ATTN_PTY_BACKEND=%s is not supported: use worker, shared or migrating, or leave it unset", selected)
	}
}

func (d *Daemon) newDedicatedWorkers() (*ptybackend.WorkerBackend, error) {
	return ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
		DataRoot:         d.dataRoot,
		DaemonInstanceID: d.daemonInstanceID,
		BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_WORKER_BINARY")),
		Logf:             d.logf,
		OnTerminalBuild:  d.handleTerminalBuildChanged,
	})
}

func (d *Daemon) startDedicatedWorkers() error {
	workers, err := d.newDedicatedWorkers()
	if err != nil {
		return fmt.Errorf("start the PTY workers: %w", err)
	}
	if shouldRunWorkerStartupProbe() {
		ctx, cancel := context.WithTimeout(context.Background(), workerStartupProbeTimeout)
		defer cancel()
		if err := workers.Probe(ctx); err != nil {
			return fmt.Errorf("start the PTY workers: startup probe failed: %w", err)
		}
	}
	d.ptyBackend = workers
	d.logf("using PTY backend: worker")
	return nil
}

func (d *Daemon) startMigratingPTYBackend() error {
	legacy, err := d.newDedicatedWorkers()
	if err != nil {
		return fmt.Errorf("start the PTY workers: %w", err)
	}
	shared, err := d.newSharedPTYHost()
	if err != nil {
		d.ptyBackend = legacy
		d.logf("shared PTY host initialization failed: %v; new sessions remain on legacy workers", err)
		d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Shared PTY host is unavailable (%v). New terminals will keep using dedicated workers.", err))
		return nil
	}

	sharedEnabled := d.on(setting.SharedPTYHostEnabled)
	useSharedForNew := sharedEnabled && shared.SharedArtifactReady()
	if sharedEnabled && !useSharedForNew && !shouldRunWorkerStartupProbe() && strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")) != "" {
		useSharedForNew = true
	}
	if candidateErr := shared.SharedCandidateError(); sharedEnabled && !useSharedForNew && candidateErr != nil {
		d.logf("shared PTY host unavailable: %v; new sessions remain on legacy workers", candidateErr)
		if d.enrollmentStatus().IsHome() && parseBooleanSetting(d.store.GetSetting(setting.SharedPTYHostEnabled.Key)) {
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Shared PTY host is unavailable (%v). New terminals will keep using dedicated workers.", candidateErr))
		}
	}
	migrating, err := ptybackend.NewMigrating(legacy, shared, useSharedForNew)
	if err != nil {
		d.logf("failed to initialize PTY migration router: %v; using legacy workers", err)
		d.ptyBackend = legacy
		return nil
	}
	d.ptyBackend = migrating
	d.sharedPTYHost = shared
	if useSharedForNew {
		d.logf("using PTY backend: migrating (existing=owner, new=shared)")
	} else {
		d.logf("using PTY backend: migrating (existing=owner, new=legacy)")
	}
	return nil
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
	if d.gitHubHostsKnown == nil {
		d.gitHubHostsKnown = make(chan struct{})
	}
	if d.startedCh == nil {
		d.startedCh = make(chan struct{})
	}
	if d.plugins == nil {
		d.plugins = newPluginRegistry()
	}
	startSucceeded := false
	if err := d.acquirePIDLock(); err != nil {
		return fmt.Errorf("acquire PID lock: %w", err)
	}
	defer func() {
		if startSucceeded {
			return
		}
		d.Stop()
	}()
	d.loginEnv.Begin()
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
	if err := d.ensureEnrollment(); err != nil {
		return fmt.Errorf("ensure enrollment record: %w", err)
	}
	d.emptyDesktops.grace = emptyDesktopGraceFromEnv()
	d.store.OnEmptyDesktop(d.desktopEmptied)
	d.removeLegacyStateFile()
	d.ensurePluginSupervisor()
	if err := d.startEventBus(); err != nil {
		return fmt.Errorf("start event bus: %w", err)
	}
	d.ensureGardenCollections()
	if err := d.adoptInlineSeedBodies(); err != nil {
		return fmt.Errorf("keep seed bodies from an older build: %w", err)
	}
	if d.ptyBackend == nil {
		if err := d.startPTYBackend(); err != nil {
			return err
		}
	}
	d.loadTerminals()
	if d.clientToken == "" {
		token, err := config.EnsureClientToken(d.dataRoot)
		if err != nil {
			return fmt.Errorf("ensure client token: %w", err)
		}
		d.clientToken = token
	}
	if err := d.dropStaleCopy(); err != nil {
		d.logf("drop the copy of terminals run for another home: %v", err)
	}
	d.applyHeadlessContextWindowCap()
	d.applyHeadlessTasksMode()
	pausepoint.At(pausepoint.DaemonEnrollmentLoaded)
	d.copyImagesOfAsksOpenedBeforeImagesTravelled()
	if err := d.recoverQuickCaptureAssets(); err != nil {
		d.logf("quick capture asset recovery incomplete: %v", err)
	}
	if err := d.startBridgeSocket(); err != nil {
		return fmt.Errorf("start bridge socket: %w", err)
	}
	if d.outpostsEnabled() {
		d.configureOutposts()
	}
	run, previous, err := d.beginRun(time.Now())
	if err != nil {
		d.logf("daemon run: %v", err)
	}
	d.run = run
	d.configureAnalytics()
	d.configurePhoneAccess()
	d.recordAnalytics(analytics.StreamSample, "daemon_start", d.run.ID, previous)
	d.ensureCrewCollections()
	d.importCrewHomes()
	d.ensureChiefs()
	d.removeEmptyDesktops()
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
	if err := d.dropStaleCopy(); err != nil {
		return fmt.Errorf("refresh enrollment before serving commands: %w", err)
	}
	listener := d.listener
	d.log("daemon started")
	d.wsHub.logf = d.logf
	for _, session := range d.store.List("") {
		d.startSessionMonitor(session.ID)
	}
	d.reapStrandedPluginRuntimes()
	d.life.Go("followLoginEnv", d.followLoginEnv)

	d.life.Go("runDesktopTileContent", func() { d.runDesktopTileContent(d.life.Done()) })
	d.life.Go("runTileDocumentWatcher", func() { d.runTileDocumentWatcher(d.life.Done()) })
	d.life.Go("tileDocumentsHere", func() { d.keepTileDocumentsWatched(d.life.Context(), "", nil) })

	if hooks, ok := d.ptyBackend.(ptybackend.LifecycleHooks); ok {
		hooks.SetExitHandler(func(info ptybackend.ExitInfo) { d.handlePTYExit(info) })
		hooks.SetStateHandler(d.handlePTYState)
	}
	if hooks, ok := d.ptyBackend.(ptybackend.TitleHooks); ok {
		hooks.SetTitleHandler(d.handlePTYTitle)
	}

	d.initHTTPServer()
	if err := d.listenHTTP(); err != nil {
		d.logf("%v", err)
		return err
	}
	httpListener := d.httpListener
	d.life.Go("runHTTPServer", func() { d.runHTTPServer(httpListener) })
	d.maybeStartDiagServer()

	gitHubCLIChecked := make(chan struct{})
	d.life.Go("refreshGitHubHosts", func() {
		defer close(d.gitHubHostsKnown)
		cliUsable := d.checkGitHubCLI()
		discover := func() {
			if err := d.discoverGitHubHosts(cliUsable); err != nil {
				d.logf("Initial GitHub host discovery failed: %v", err)
			}
		}
		if mockGitHubURL() != "" {
			discover()
			close(gitHubCLIChecked)
		} else {
			close(gitHubCLIChecked)
			discover()
		}
		d.life.Go("pollPRs", d.pollPRs)
		d.life.Go("refreshGitHubHostsLoop", d.refreshGitHubHostsLoop)
	})
	recoveryStartedAt := time.Now()

	d.life.Go("monitorBranches", d.monitorBranches)

	queueStarted := make(chan error, 1)
	if !d.life.Go("jobQueue", func() {
		home := d.enrollmentStatus().IsHome()
		err := d.startJobQueue(home)
		queueStarted <- err
		if err != nil {
			return
		}
		for {
			select {
			case <-d.life.Done():
				d.jobQueueRef().Stop()
				return
			case <-d.jobQueueRoleChanged:
				if current := d.enrollmentStatus().IsHome(); current != home {
					d.jobQueueRef().Stop()
					if err := d.startJobQueue(current); err != nil {
						d.logf("background jobs after enrollment change: %v", err)
					}
					home = current
				}
			}
		}
	}) {
		return errDaemonStopping
	}
	if err := <-queueStarted; err != nil {
		return err
	}
	if !d.noAgentSockets {
		if err := d.claudeMod().serve(); err != nil {
			d.logf("claude mod: live sessions are typed into: %v", err)
		}
	}

	d.watchRecoveredLaunches()
	d.life.Go("startupRecovery", func() {
		pausepoint.At(pausepoint.DaemonStartupRecovery)
		if d.stopping() {
			d.logf("startup recovery skipped: the daemon is stopping; the next daemon recovers")
			return
		}
		d.performStartupPTYRecovery(previousRunSessions, recoveryStartedAt)
		d.claudeMod().expectModsToReconnect()
		d.reconcileOutpostCommandTerminals()
		if d.stopping() {
			return
		}
		d.upgradeStaleTerminals()
		d.monitors().registry.ResolveDue(time.Now())
		d.life.Go("runSessionResolver", d.monitors().registry.Run)
		if _, routed := d.ptyBackend.(*ptybackend.MigratingBackend); routed {
			d.life.Go("validateSharedPTYHostAfterRecovery", d.validateSharedPTYHostAfterRecovery)
		} else {
			d.validateSharedPTYHostAfterRecovery()
		}
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
		select {
		case <-gitHubCLIChecked:
		case <-d.life.Done():
			return
		}
		if !d.recoverAutomationsAtStart() {
			return
		}
		d.setRecovering(false)
		d.startPermanentMaintenance()
		d.resumePendingDelegations()
		d.reviveRecoverableAgents("")
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
		if _, fromPreviousRun := previousRunSessions[session.ID]; !fromPreviousRun || session.Host != nil {
			continue
		}
		if _, ok := liveIDs[session.ID]; ok || d.hidden(session.ID) {
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
			d.applyState(session.ID, sessionmonitor.Change{
				State: string(protocol.SessionStateRecoverable),
				Cause: sessionmonitor.StartupRecovery{},
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
	if host := d.sessionHost(session.ID); host != "" {
		readiness, err := d.placedConversationReadiness(session.ID, host, session.Agent, session.ProfileID, resumeID, false)
		if err != nil {
			d.logf("recovery: keeping session %s after conversation lookup on %s failed: %v", session.ID, host, err)
			return true
		}
		if readiness.Known {
			return true
		}
	} else if d.conversationKnown(driver, resumeID) {
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

	d.recoverLinkProcesses(context.Background())
	if _, ok := d.ptyBackend.(ptybackend.RecoverableRuntime); ok {
		d.reconcileStartupWorkerSessions(recoveryReport, recoverErr, previousRunSessions, recoveryStartedAt)
		d.recoverLinkViews(context.Background())
		d.restoreTranscriptWatchers()
		d.replayPendingConversations()
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
	d.recoverLinkViews(context.Background())
	d.pruneRuntimesWithoutSession(context.Background())
	d.restoreTranscriptWatchers()
}

func (d *Daemon) pruneRuntimesWithoutSession(ctx context.Context) {
	if d.store == nil {
		return
	}
	for terminal := range d.liveTerminals(ctx) {
		if _, shown := d.shownIn(terminal); shown || d.linkProcess(terminal) {
			continue
		}
		pending := false
		for _, raw := range d.store.PendingSessionConversations() {
			for _, conversation := range decodePendingConversations(raw) {
				pending = pending || conversation.Terminal == terminal
			}
		}
		if pending {
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

	reconcile := d.recoverSessions(context.Background(), recoveryRules{
		allowIdleDemotion:     allowIdleDemotion,
		allowTombstoneCleanup: allowIdleDemotion,
		previousRunSessions:   previousRunSessions,
		startedAt:             recoveryStartedAt,
	})
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

type recoveryRules struct {
	host                  string
	allowIdleDemotion     bool
	allowTombstoneCleanup bool
	previousRunSessions   map[protocol.SessionID]struct{}
	placements            map[protocol.SessionID][]harness.TerminalID
	startedAt             time.Time
}

type machineReport struct {
	live         map[protocol.SessionID]liveTerminal
	probes       map[protocol.SessionID]livenessProbe
	stateUpdated []protocol.SessionID
}

type liveTerminal struct {
	terminal harness.TerminalID
	link     bool
	info     ptybackend.SessionInfo
	haveInfo bool
}

type livenessProbe struct {
	likelyAlive bool
	err         error
}

func (d *Daemon) recoverSessions(ctx context.Context, rules recoveryRules) workerReconcileReport {
	if d.store == nil || d.ptyBackend == nil {
		return workerReconcileReport{}
	}
	machine := d.recoverMachine(ctx)
	var sessions []protocol.SessionID
	for _, session := range d.store.List("") {
		if session.Host == nil {
			sessions = append(sessions, session.ID)
		}
	}
	return d.recoverRecords(machine, sessions, rules)
}

func (d *Daemon) placedLiveTerminals(ctx context.Context) map[protocol.SessionID]liveTerminal {
	placed := make(map[protocol.SessionID]liveTerminal)
	for terminalID := range d.liveTerminals(ctx) {
		sessionID, ok := d.terminals().Showing(terminalID)
		if !ok {
			d.logf("worker reconciliation left runtime %s to the orphan prune: no pane places it", terminalID)
			continue
		}
		placed[sessionID] = liveTerminal{terminal: terminalID, link: d.linkProcess(terminalID)}
	}
	return placed
}

func (d *Daemon) recoverMachine(ctx context.Context) machineReport {
	report := machineReport{live: d.placedLiveTerminals(ctx), probes: make(map[protocol.SessionID]livenessProbe)}

	infoProvider, _ := d.ptyBackend.(ptybackend.SessionInfoProvider)
	for sessionID, live := range report.live {
		if live.link {
			continue
		}
		existing := d.store.Get(sessionID)
		if existing == nil && d.store.SessionClosed(sessionID) {
			continue
		}
		if infoProvider != nil {
			if info, err := infoProvider.SessionInfo(ctx, live.terminal); err == nil {
				d.handlePTYTitle(live.terminal, info.Title)
				live.info, live.haveInfo = info, true
				report.live[sessionID] = live
			}
		}
		if existing != nil && d.resumeMonitor(existing, live) {
			report.stateUpdated = append(report.stateUpdated, sessionID)
		}
	}

	if prober, ok := d.ptyBackend.(ptybackend.SessionLivenessProber); ok {
		for _, session := range d.store.List("") {
			if _, live := report.live[session.ID]; live {
				continue
			}
			likelyAlive, err := prober.SessionLikelyAlive(ctx, d.primaryTerminal(session.ID))
			report.probes[session.ID] = livenessProbe{likelyAlive: likelyAlive, err: err}
		}
	}
	return report
}

func (d *Daemon) resumeMonitor(existing *protocol.Session, live liveTerminal) (stateUpdated bool) {
	sessionID := existing.ID
	monitor := d.startSessionMonitor(sessionID)
	d.touchSession(sessionID)
	if existing.State == protocol.SessionStateWaitingInput && !protocol.Deref(existing.ConversationStarted) && monitor.ResumeAtFolderTrustQuestion(existing.Agent, live.terminal) {
		return false
	}
	if existing.State == protocol.SessionStateScheduled || !live.haveInfo {
		return false
	}
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" &&
		(d.pluginDriverReportsState(existing.Agent) ||
			existing.State == protocol.SessionStateWaitingInput ||
			existing.State == protocol.SessionStatePendingApproval) {
		return false
	}
	nextState, ok := sessionStateFromRecoveredInfo(live.info)
	if !ok && !sessionmonitor.ResolverOwnedStates[existing.State] {
		nextState, ok = protocol.SessionStateLaunching, true
	}
	if ok && existing.State != nextState {
		d.applyState(sessionID, sessionmonitor.Change{
			State: string(nextState),
			Cause: sessionmonitor.StartupRecovery{},
		})
		stateUpdated = true
	}
	d.seedRecoveredEvidence(sessionID, existing, live.info)
	return stateUpdated
}

func (d *Daemon) recoverRecords(machine machineReport, sessions []protocol.SessionID, rules recoveryRules) workerReconcileReport {
	report := workerReconcileReport{}
	for _, sessionID := range machine.stateUpdated {
		report.StateUpdated++
		report.markChanged(sessionID)
	}

	for sessionID, live := range machine.live {
		if live.link {
			continue
		}
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
		if existing != nil {
			d.store.ClearSessionIntentionalClose(sessionID)
			continue
		}
		if d.store.SessionClosed(sessionID) {
			d.logf("worker reconciliation stopped runtime %s: its session is closed", sessionID)
			d.terminateSession(sessionID, syscall.SIGTERM)
			continue
		}
		d.adoptLiveTerminal(sessionID, live, &report)
	}

	if rules.allowTombstoneCleanup {
		for _, sessionID := range d.store.SessionTeardownIntentIDs() {
			host, copied := d.store.SessionHost(sessionID)
			if host != rules.host || copied {
				continue
			}
			if _, live := machine.live[sessionID]; live {
				continue
			}
			existing := d.store.Get(sessionID)
			teardown := d.resumeSessionTeardown(sessionID)
			if teardown != nil {
				notified := d.notifyPreparedPluginDriverSessionClosed(sessionID, teardown, syscall.SIGTERM)
				if notified {
					d.store.ClearSessionIntentionalClose(sessionID)
				}
				teardown.releaseLifecycle()
				if !notified {
					continue
				}
			}
			if existing != nil {
				report.Reaped++
				report.markChanged(sessionID)
			}
		}
	}

	for _, sessionID := range sessions {
		d.recoverMissingSession(sessionID, machine, rules, &report)
	}

	return report
}

func (d *Daemon) recoverMissingSession(sessionID protocol.SessionID, machine machineReport, rules recoveryRules, report *workerReconcileReport) {
	if rules.placements != nil {
		release := d.acquireSpawnLock(sessionID)
		defer release()
		if !slices.Equal(rules.placements[sessionID], d.terminalsOf(sessionID)) {
			report.SkippedRecent++
			return
		}
	}

	if _, fromPreviousRun := rules.previousRunSessions[sessionID]; !fromPreviousRun {
		return
	}
	if _, ok := machine.live[sessionID]; ok || d.hidden(sessionID) {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	if sessionUpdatedAfter(session, rules.startedAt) {
		report.SkippedRecent++
		return
	}
	if probe, probed := machine.probes[sessionID]; probed {
		if probe.err != nil {
			d.logf("worker liveness probe failed for session %s: %v", sessionID, probe.err)
			report.LivenessUnknown++
			return
		}
		if probe.likelyAlive {
			report.LikelyAlive++
			return
		}
	}
	if !rules.allowIdleDemotion {
		report.SkippedIdle++
		return
	}
	d.releaseExitedCrewBinding(sessionID)
	if d.store.GetSessionExit(sessionID) != nil {
		return
	}
	if d.canReviveSession(session) {
		if session.State == protocol.SessionStateRecoverable {
			return
		}
		d.recoverSessionState(sessionID)
		report.StateUpdated++
		report.MarkedRecoverable++
		report.markChanged(sessionID)
	} else {
		d.removeReapedSession(sessionID)
		report.Reaped++
		report.markChanged(sessionID)
	}
}

func (d *Daemon) adoptLiveTerminal(sessionID protocol.SessionID, live liveTerminal, report *workerReconcileReport) {
	if !live.haveInfo {
		report.MissingMetadata++
		return
	}
	info := live.info
	if normalizeSpawnAgent(info.Agent) == protocol.AgentShellValue {
		report.SkippedShell++
		return
	}

	now := string(protocol.TimestampNow())
	directory := strings.TrimSpace(info.CWD)
	if directory == "" {
		report.MissingMetadata++
		return
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
		return
	}
	recoveredSession := &protocol.Session{
		ID:             sessionID,
		Label:          label,
		Agent:          normalizeSpawnAgent(info.Agent),
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
		return
	}
	d.startSessionMonitorSeeded(recoveredSession, sessionmonitor.Evidence{})
	report.Created++
	report.markChanged(sessionID)
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

		reconcile := d.recoverSessions(context.Background(), recoveryRules{
			allowIdleDemotion:     true,
			allowTombstoneCleanup: fullyRecovered,
			previousRunSessions:   previousRunSessions,
			startedAt:             recoveryStartedAt,
		})
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
		d.reviveRecoverableAgents("")
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

func sessionStateFromRecoveredInfo(info ptybackend.SessionInfo) (protocol.SessionState, bool) {
	if !info.Running {
		return protocol.SessionStateIdle, true
	}
	agent := normalizeSpawnAgent(info.Agent)
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
	d.claudeMod().close()
	// Work begun before the stop finishes; later exits and failures are the teardown's, not outcomes.
	// PTYs shut next so no real exit lands between the drain and the shutdown.
	d.life.wait(d.logf)
	d.stopMonitorCatchUps()
	d.closeGitExecution(ErrGitExecutorClosed)
	if d.ptyBackend != nil {
		_ = d.ptyBackend.Shutdown(context.Background())
	}
	d.monitors().registry.Close()
	d.stopRootWatchers()
	d.stopEventBus()
	d.stopInstalledPlugins()
	d.monitors().registry.StopTranscriptWatchers()

	d.stopInbox()
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

var errCallerGone = errors.New("the caller disconnected")

func peerClosed(conn net.Conn) <-chan struct{} {
	closed := make(chan struct{})
	goTransport(func() {
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	})
	return closed
}

func (d *Daemon) handlePTYExit(info ptybackend.ExitInfo) bool {
	release, held := d.life.Hold("handlePTYExit")
	if !held {
		d.logf("pty exit of %s during daemon stop is not an outcome; the next daemon recovers the session", info.ID)
		return false
	}
	defer release()
	defer pausepoint.At(pausepoint.PtyExitHandled)
	if d.exitOfSupersededRuntime(info) {
		d.logf("ignoring exit of a replaced runtime in terminal %s", info.ID)
		return false
	}
	d.linkTerminalExited(info.ID)
	if d.linkProcess(info.ID) {
		d.forgetTerminalTitle(info.ID)
		return false
	}
	d.bindMonitorTerminals()
	owner, bound, shown := d.terminals().takeExiting(info.ID)
	sessionID := owner.session
	pausepoint.At(pausepoint.PtyExitOwnerResolved)
	if !bound {
		d.forgetTerminalTitle(info.ID)
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
	if d.sessionMonitor(sessionID).SupersededExitDuringDriverLaunch(info) {
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
	if !bound {
		d.endClosedSessionsRun(sessionID, owner, info)
		return true
	}
	if session := d.store.Get(sessionID); session != nil {
		driver := agentdriver.Get(session.Agent)
		if driver != nil && !agentdriver.EffectiveCapabilities(driver).HasHooks {
			if finder, ok := agentdriver.GetTranscriptFinder(driver); ok {
				path := finder.FindTranscriptForResume(d.store.GetResumeSessionID(sessionID))
				receipt, accepted := d.observeStoppedConversation(sessionID, d.resolveStopTranscriptPath(session, path))
				if accepted {
					if err := receipt.Wait(d.life.Context()); err != nil {
						d.logf("exit conversation: session=%s: %v", sessionID, err)
					}
				}
			}
		}
	}
	unlockEnds := d.lockTerminalEnds(sessionID)
	if d.exitOfSupersededRuntime(info) {
		unlockEnds()
		d.logf("ignoring exit of a replaced runtime in terminal %s", info.ID)
		return false
	}
	stopped := d.consumeTerminalExitIntent(info.ID, terminalExitStop)
	if !d.endTerminal(sessionID, info.ID) {
		unlockEnds()
		d.logf("terminal %s exited; session %s runs on in its other terminals", info.ID, sessionID)
		return true
	}
	if info.RuntimeLost() && !stopped && d.lostRuntimeCanComeBack(sessionID) {
		unlockEnds()
		d.bringBackLostRuntime(sessionID, info)
		return true
	}
	monitor := d.sessionMonitor(sessionID)
	exit, held := monitor.HeldExit(info.LifecycleID)
	if !held {
		exit = d.holdExit(sessionID, info)
	}
	monitor.FenceInput()
	monitor.StopTranscriptWatchers()
	reason := "exited"
	if stopped {
		reason = "killed"
	}
	d.closePluginDriverSession(sessionID, reason, &info.ExitCode, info.Signal)
	d.noteLaunchExited(sessionID, exit)
	if err := d.removePTYSession(info.ID); err != nil {
		d.logf("pty backend remove on exit failed for %s: %v", info.ID, err)
	}
	unlockEnds()
	pausepoint.At(pausepoint.PtyExitRuntimeRemoved)
	exit.Held, exit.Stopped = false, stopped
	if monitor == nil || d.store.Get(sessionID) == nil {
		if owner.copied || d.copiedSession(sessionID) {
			return true
		}
		d.reactSessionExit(sessionID, exit)
		return true
	}
	d.reportExit(sessionID, exit)
	return true
}

func (d *Daemon) lostRuntimeCanComeBack(sessionID protocol.SessionID) bool {
	session := d.store.Get(sessionID)
	if session == nil || d.copiedSession(sessionID) || !d.canReviveSession(session) {
		return false
	}
	if session.Agent != protocol.AgentShellValue && !d.agentAvailable(string(session.Agent)) {
		return false
	}
	intent, _ := d.store.LaunchIntent(sessionID)
	return intent.Command == nil
}

func (d *Daemon) bringBackLostRuntime(sessionID protocol.SessionID, info ptybackend.ExitInfo) {
	monitor := d.sessionMonitor(sessionID)
	monitor.ResetInput()
	monitor.StopTranscriptWatchers()
	d.closePluginDriverSession(sessionID, "exited", &info.ExitCode, info.Signal)
	if err := d.removePTYSession(info.ID); err != nil {
		d.logf("pty backend remove on lost runtime failed for %s: %v", info.ID, err)
	}
	d.applyState(sessionID, sessionmonitor.Change{State: string(protocol.SessionStateRecoverable), Cause: sessionmonitor.RuntimeLost{}})
	d.logf("terminal %s lost its runtime; bringing session %s back", info.ID, sessionID)
	cols, rows := 80, 24
	if size, sized := d.ptySizes.owned(protocol.TerminalID(info.ID)); sized {
		cols, rows = size.Cols, size.Rows
	}
	if err := d.reviveSession(sessionID, cols, rows); err != nil {
		d.logf("session %s lost its runtime and could not come back: %v", sessionID, err)
		monitor.FenceInput()
		d.reportExit(sessionID, d.exitOf(sessionID, info))
	}
}

func (d *Daemon) endClosedSessionsRun(sessionID protocol.SessionID, owner endingTerminal, info ptybackend.ExitInfo) {
	exit := d.exitOf(sessionID, info)
	exit.Stopped = d.consumeTerminalExitIntent(info.ID, terminalExitStop)
	if err := d.removePTYSession(info.ID); err != nil {
		d.logf("pty backend remove on exit failed for %s: %v", info.ID, err)
	}
	reopened := d.store.Get(sessionID) != nil
	if reopened || owner.copied || d.copiedSession(sessionID) {
		return
	}
	d.announceRunExit(sessionID, exit)
}

func (d *Daemon) reactSessionExit(sessionID protocol.SessionID, exit sessionmonitor.Exit) {
	d.releaseExitedCrewBinding(sessionID)
	commandTerminal := d.announceRunExit(sessionID, exit)
	if commandTerminal {
		return
	}
	if exit.Code == 0 && exit.Signal == "" && !exit.Stopped {
		d.life.Go("closeNormallyExitedSession", func() { d.closeNormallyExitedSession(sessionID) })
	}
}

func (d *Daemon) announceRunExit(sessionID protocol.SessionID, exit sessionmonitor.Exit) (commandTerminal bool) {
	d.noteLaunchExited(sessionID, exit)
	commandTerminal = d.finishCommandTerminal(sessionID, exit)
	d.publishFact(FactSessionPTYExited, string(sessionID), ptyExit{
		Terminal: string(exit.Terminal),
		ExitCode: exit.Code,
		Signal:   exit.Signal,
	})
	d.sessionMonitor(sessionID).RecordExitPublished(exit)
	pausepoint.At(pausepoint.SessionExitObserved)
	d.broadcastSessionStateChanged(sessionID)
	return commandTerminal
}

func (d *Daemon) closeNormallyExitedSession(sessionID protocol.SessionID) {
	if d.sessionCloseError(sessionID) != nil {
		return
	}
	closing, err := d.beginSessionClose(sessionID, store.SessionClose{By: who.Session(sessionID).Actor(), Reason: "Agent exited normally"}, nil)
	if err != nil {
		d.logf("closing normally exited session %s: %v", sessionID, err)
		return
	}
	d.finishSessionClose(sessionID, closing)
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
	err := d.removeTerminalRuntime(ctx, terminal)
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
			retryErr := d.removeTerminalRuntime(ctx, terminal)
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
		d.sessionMonitor(sessionID).MarkForcedStop()
		if d.store != nil {
			if markErr := d.store.MarkSessionIntentionalClose(sessionID, time.Now()); markErr != nil {
				d.logf("session teardown intent failed for %s: %v", sessionID, markErr)
			}
		}
		d.sessionMonitor(sessionID).StopTranscriptWatchers()
		if d.ptyBackend != nil {
			for _, terminal := range d.terminalsOf(sessionID) {
				_ = d.removeTerminalRuntime(context.Background(), terminal)
			}
		}
	}
}

func (d *Daemon) markSessionTerminationIntent(sessionID protocol.SessionID) error {
	if err := d.requireLiveSessionHost(sessionID); err != nil {
		return err
	}
	d.sessionMonitor(sessionID).MarkForcedStop()
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
		d.sessionMonitor(sessionID).ClearForcedStop()
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
		d.sessionMonitor(sessionID).StopTranscriptWatchers()
		return nil
	}
	for _, terminal := range terminals {
		_, err := Call[struct{}](context.Background(), d.router(), protocol.TerminalKillMessage{Cmd: protocol.CmdTerminalKill, TerminalID: terminal, Signal: signalName(sig), EndingSessionID: protocol.Ptr(sessionID)})
		if err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
			return fmt.Errorf("kill terminal %s: %w", terminal, err)
		}
	}
	d.sessionMonitor(sessionID).StopTranscriptWatchers()
	for _, terminal := range terminals {
		if err := d.removeTerminalRuntime(context.Background(), terminal); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove terminal %s: %w", terminal, err)
		}
	}
	return nil
}

func (d *Daemon) prepareSessionTeardown(sessionID protocol.SessionID) (*sessionTeardown, error) {
	lifecycleLock := d.sessionLifecycleLockFor(sessionID)
	lifecycleLock.Lock()
	if err := d.requireLiveSessionHost(sessionID); err != nil {
		lifecycleLock.Unlock()
		return nil, err
	}
	session := d.store.Get(sessionID)
	if session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before session removal: %v", sessionID, err)
		}
	}
	var prepared protocol.AgentLaunchCloseResult
	if session != nil && session.Agent == protocol.AgentShellValue {
		d.sessionMonitor(sessionID).MarkForcedStop()
	}
	if session != nil && session.Agent != protocol.AgentShellValue && d.unreachableSessionHost(sessionID) == "" {
		var err error
		prepared, err = d.agentCloseCall(protocol.AgentLaunchCloseMessage{SessionID: sessionID, Phase: "prepare"})
		if err != nil {
			lifecycleLock.Unlock()
			return nil, err
		}
	}
	var retired sessionmonitor.DriverMetadata
	if prepared.Driver != nil {
		if err := json.Unmarshal([]byte(*prepared.Driver), &retired); err != nil {
			lifecycleLock.Unlock()
			return nil, err
		}
	}
	driverRun, err := d.store.PrepareSessionTeardown(sessionID, d.primaryTerminal(sessionID), store.AgentDriverReportCursor{PluginName: retired.Plugin, RunID: retired.Run, Seq: retired.Sequence}, time.Now())
	if err != nil {
		_, _ = d.agentCloseCall(protocol.AgentLaunchCloseMessage{SessionID: sessionID, Phase: "cancel", Driver: prepared.Driver})
		lifecycleLock.Unlock()
		return nil, err
	}
	pausepoint.At(pausepoint.SessionTeardownPrepared)
	return &sessionTeardown{session: session, terminals: d.terminalsOf(sessionID), driverRun: driverRun, lifecycleLock: lifecycleLock}, nil
}

func (d *Daemon) commitSessionUnregister(sessionID protocol.SessionID, closed store.SessionClose) error {
	return d.closeSession(sessionID, closed)
}

func (d *Daemon) cancelSessionTeardown(sessionID protocol.SessionID, teardown *sessionTeardown) {
	defer teardown.releaseLifecycle()
	if session := d.store.Get(sessionID); session != nil && session.Agent == protocol.AgentShellValue {
		d.sessionMonitor(sessionID).ClearForcedStop()
	} else {
		run := d.store.SessionTeardownDriverRun(sessionID)
		raw, _ := json.Marshal(sessionmonitor.DriverMetadata{Plugin: run.PluginName, Run: run.RunID, Sequence: run.Seq})
		if _, err := d.agentCloseCall(protocol.AgentLaunchCloseMessage{SessionID: sessionID, Phase: "cancel", Driver: protocol.Ptr(string(raw))}); err != nil {
			d.logf("cancel machine teardown: %v", err)
		}
	}
	if err := d.store.CancelSessionTeardown(sessionID); err != nil {
		d.logf("cancel session teardown failed for %s: %v", sessionID, err)
	}
}

func (d *Daemon) resumeSessionTeardown(sessionID protocol.SessionID) *sessionTeardown {
	lifecycleLock := d.sessionLifecycleLockFor(sessionID)
	lifecycleLock.Lock()
	if d.sessionTeardownInFlight(sessionID) {
		lifecycleLock.Unlock()
		return nil
	}
	driverRun, found, err := d.store.PrepareExistingSessionTeardown(sessionID, time.Now())
	if err != nil {
		lifecycleLock.Unlock()
		d.logf("session teardown recovery failed for %s: %v", sessionID, err)
		return nil
	}
	if !found {
		lifecycleLock.Unlock()
		return nil
	}
	session := d.store.Get(sessionID)
	terminals := d.terminalsOf(sessionID)
	if err := d.closeSession(sessionID, store.SessionClose{By: who.User()}); err != nil {
		lifecycleLock.Unlock()
		d.logf("session teardown recovery close failed for %s: %v", sessionID, err)
		return nil
	}
	return &sessionTeardown{session: session, terminals: terminals, driverRun: driverRun, lifecycleLock: lifecycleLock}
}

func (d *Daemon) notifyPreparedPluginDriverSessionClosed(sessionID protocol.SessionID, teardown *sessionTeardown, sig syscall.Signal) bool {
	run := teardown.driverRun
	if run.RunID == "" {
		return true
	}
	crashpoint.At("session-close-before-driver-notification")
	claimed, err := d.store.ClaimSessionTeardownDriverRun(sessionID, run.RunID)
	if err != nil {
		d.logf("plugin session close claim failed: session=%s run=%s error=%v", sessionID, run.RunID, err)
		return false
	}
	if claimed {
		if run.TerminalID != "" {
			raw, _ := json.Marshal(sessionmonitor.DriverMetadata{Plugin: run.PluginName, Run: run.RunID, Sequence: run.Seq})
			if _, err := d.agentCloseCall(protocol.AgentLaunchCloseMessage{SessionID: sessionID, Phase: "notify", TerminalID: protocol.Ptr(run.TerminalID), Driver: protocol.Ptr(string(raw)), Signal: protocol.Ptr(signalName(sig))}); err != nil {
				d.logf("notify machine teardown: %v", err)
				return false
			}
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
		crashpoint.At(crashBeforeTeardownKill)
		pausepoint.At(pausepoint.SessionTeardownKilling)
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
		if d.notifyPreparedPluginDriverSessionClosed(sessionID, teardown, sig) {
			d.store.ClearSessionIntentionalClose(sessionID)
		}
	}
	// A close the user asked for still ends the session while the daemon stops, on the caller's goroutine.
	if !d.life.Go("terminateSessionAsync", terminate) {
		terminate()
	}
	if teardown != nil && teardown.session != nil {
		if l := d.linkOf(sessionID); l != nil {
			profile := teardown.session.ProfileID
			d.life.Go("linkReleasedAfterClose", func() {
				<-done
				l.profileReleased(profile)
			})
		}
	}
	return done
}

func (d *Daemon) closeSession(sessionID protocol.SessionID, closed store.SessionClose) error {
	if d.unreachableSessionHost(sessionID) != "" {
		return d.closeSessionRecord(sessionID, closed)
	}
	if session := d.store.Get(sessionID); session != nil && session.Agent == protocol.AgentShellValue {
		return d.closeSessionRecord(sessionID, closed)
	}
	if err := d.drainAgentBeforeClose(sessionID); err != nil {
		return err
	}
	session := d.store.Get(sessionID)
	link := d.linkOf(sessionID)
	if err := d.closeSessionRecord(sessionID, closed); err != nil {
		return err
	}
	if _, err := d.agentCloseCall(protocol.AgentLaunchCloseMessage{SessionID: sessionID, Phase: "committed"}); err != nil {
		d.logf("machine close: %v", err)
	}
	if link != nil && session != nil {
		profile, conversation := session.ProfileID, d.store.GetSessionConversation(sessionID).NativeID
		d.life.Go("linkAfterSessionClose", func() {
			lifecycle := d.sessionLifecycleLockFor(sessionID)
			lifecycle.Lock()
			defer lifecycle.Unlock()
			link.sessionClosed(sessionID, profile, conversation)
		})
	}
	return nil
}

func (d *Daemon) closeSessionRecord(sessionID protocol.SessionID, closed store.SessionClose) error {
	if session := d.store.Get(sessionID); session != nil && session.Agent == protocol.AgentShellValue {
		d.decorateSessionWithTitle(session)
		closed.FallbackLabel = protocol.Deref(session.Title)
		if closed.FallbackLabel == "" {
			closed.FallbackLabel = store.DefaultSessionLabel(session.Directory, sessionID)
		}
	}
	return d.recordSessionClose(sessionID, d.store.Get(sessionID), func() (store.SessionClosure, error) { return d.store.CloseSession(sessionID, closed, time.Now()) })
}

func (d *Daemon) recordSessionClose(sessionID protocol.SessionID, closing *protocol.Session, commit func() (store.SessionClosure, error)) error {
	terminals := d.terminalsOf(sessionID)
	defer d.announceUnplacement(sessionID)()
	if session := d.store.Get(sessionID); session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before closing it: %v", sessionID, err)
		}
	}
	d.kickInboxAfterCommit(who.ToSession(sessionID))
	d.clearSnoozeState(sessionID)
	closure, err := commit()
	if err != nil {
		d.logf("close session %s: %v", sessionID, err)
		return err
	}
	unregistered := d.sessionForBroadcast(closing)
	if _, pending := d.sessionMonitor(sessionID).PendingConversation(); !pending {
		d.forgetMonitor(sessionID)
	}
	d.announceClosedAsks(closure.Asks)
	if closure.Recorded {
		for _, terminal := range terminals {
			d.forgetTerminalTitle(terminal)
		}
		d.queueConversationKeep()
		d.invalidateGardenSeedParties("session close")
		if entry := d.store.SessionLedgerEntry(sessionID); entry != nil {
			d.decorateLedgerEntryWithUsage(entry)
			d.publishFact(FactSessionClosed, string(sessionID), entry)
		}
	}
	if unregistered != nil {
		d.publishFact(FactSessionUnregistered, string(sessionID), unregistered)
	}
	d.releaseCrewBindingIfSession(sessionID)
	d.crewMemo().forget(sessionID)
	return nil
}

func (d *Daemon) removeReapedSession(sessionID protocol.SessionID) {
	terminals := d.terminalsOf(sessionID)
	defer d.announceUnplacement(sessionID)()
	if session := d.store.Get(sessionID); session != nil {
		if _, err := d.captureGardenSessionExecution(session); err != nil {
			d.logf("garden: preserving execution %s before reaping: %v", sessionID, err)
		}
	}
	d.kickInboxAfterCommit(who.ToSession(sessionID))
	d.clearSnoozeState(sessionID)
	d.sessionMonitor(sessionID).FenceInput()
	closedAsks := d.store.Remove(sessionID)
	for _, terminal := range terminals {
		d.forgetTerminalTitle(terminal)
	}
	d.forgetMonitor(sessionID)
	d.announceClosedAsks(closedAsks)
	d.releaseCrewBindingIfSession(sessionID)
}

func (d *Daemon) handlePTYState(terminal harness.TerminalID, obs pty.Observation) {
	sessionID, shown := d.shownIn(terminal)
	if !shown {
		return
	}
	state := obs.Claim
	origin := sessionmonitor.Origin{Source: string(obs.Source), Detail: obs.Detail, ObservedAt: obs.At}
	monitor := d.monitorInTerminal(terminal, sessionID)
	evidenceChanged := monitor.RecordPTY(obs)
	if obs.Source == pty.SourceProgramStatus && obs.Claim == pty.ProgramWorking {
		d.codexShared().checkWorkingSoon(sessionID)
	}
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
	if sessionmonitor.ReportsTurnStarts(string(agent)) {
		d.traceStateVeto(sessionID, origin, state, "harness_reports_its_own_start")
		return
	}
	d.logf(
		"pty state update: session=%s agent=%s state=%s source=%s detail=%q observed_at=%s",
		sessionID, agent, state, obs.Source, obs.Detail, obs.At.Format(time.RFC3339Nano),
	)
	d.applyState(sessionID, sessionmonitor.Change{
		State:  state,
		Cause:  sessionmonitor.LiveSignal{},
		Origin: origin,
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
	d.httpHandler = d.refuseRebindableHosts(mux)

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
		return wsBindError(addr, err)
	}
	d.httpListener = listener
	return nil
}

func wsBindError(addr string, err error) error {
	instance := config.Instance()
	label := config.InstanceLabel()
	_, port, _ := net.SplitHostPort(addr)
	if holders := config.InstancesHoldingWSPort(port, instance); len(holders) > 0 {
		return fmt.Errorf(
			"refusing to start: cannot bind the WebSocket address %s for instance %q: %w. "+
				"Port %s is also used by attn instance: %s. "+
				"Each instance keeps its port in ws-port in its data dir. "+
				"Run `attn instance claim-port --instance <name>` for whichever of this instance or those has no ws-port yet, then reinstall its app. "+
				"Or stop the other instance",
			addr, label, err, port, strings.Join(holders, ", "),
		)
	}
	return fmt.Errorf(
		"refusing to start: cannot bind the WebSocket address %s for instance %q: %w. "+
			"No other attn instance on this machine uses that port, so the usual owner is a daemon for the same instance running somewhere else — "+
			"notably on a VM whose listener OrbStack forwards onto host localhost while the host port is free. "+
			"Starting anyway would leave this daemon split-brained: the app routes by WebSocket port and would attach to the foreign listener, "+
			"the CLI routes by the unix socket and would talk to this process, and every command sent from the app would silently miss these sessions. "+
			"Free %s (stop whatever holds it, including a forwarding VM) or run this daemon under another instance with ATTN_INSTANCE",
		addr, label, err, addr,
	)
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
	stats := diag.Stats{WSAddr: d.httpListener.Addr().String(), PtyBackend: d.ptyBackendMode(), DocSubscriptions: d.documentSubscriptionCount()}
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

const gitHubHostsTimer stepclock.Name = "daemon-github-hosts"

func (d *Daemon) refreshGitHubHostsLoop() {
	d.every(gitHubHostsTimer, 5*time.Minute, func() {
		if err := d.refreshGitHubHosts(); err != nil {
			d.logf("GitHub host refresh failed: %v", err)
		}
		d.recoverAutomations()
	})
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

func (d *Daemon) checkGitHubCLI() bool {
	if mockGitHubURL() != "" || gitHubPollingOffReason() != "" {
		return true
	}
	if err := github.RequireGHVersion("2.81.0"); err != nil {
		code, message := ghVersionWarning(err)
		d.logf("gh CLI unavailable (need 2.81.0+): %v", err)
		d.addWarning(code, message)
		return false
	}
	return true
}

func (d *Daemon) refreshGitHubHosts() error {
	return d.discoverGitHubHosts(d.checkGitHubCLI())
}

func (d *Daemon) discoverGitHubHosts(cliUsable bool) error {
	if d.ghRegistry == nil {
		d.ghRegistry = github.NewClientRegistry()
	}
	hostsBefore := d.gitHubHosts()

	if mockURL := mockGitHubURL(); mockURL != "" {
		if err := d.registerMockClient(mockURL); err != nil {
			d.logf("Mock GitHub client not available: %v", err)
		} else {
			d.forgetPRsOfHostsOtherThan(d.gitHubHosts())
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

	if !cliUsable {
		return nil
	}
	pausepoint.At(pausepoint.DaemonGitHubHostDiscovery)

	hosts, err := github.DiscoverHosts(d.life.Context())
	if err != nil {
		d.logf("GitHub host discovery failed: %v", err)
		return nil
	}

	discovered := make(map[string]bool)
	var reported []string
	for _, hostInfo := range hosts {
		if hostInfo.Host == "" {
			continue
		}
		reported = append(reported, hostInfo.Host)
		if !hostInfo.Usable {
			d.logf("GitHub host %s is signed in but gh reports it unusable", hostInfo.Host)
			continue
		}
		token, err := github.GetTokenForHost(d.life.Context(), hostInfo.Host)
		if err != nil {
			d.logf("GitHub token fetch failed for %s: %v", hostInfo.Host, err)
			continue
		}
		client, err := github.NewClientForHost(d.life.Context(), hostInfo.Host, hostInfo.APIURL, token)
		if err != nil {
			d.logf("GitHub client create failed for %s: %v", hostInfo.Host, err)
			continue
		}
		d.registerGitHubClient(hostInfo.Host, client)
		discovered[hostInfo.Host] = true
		pausepoint.At(pausepoint.DaemonGitHubHostRegistered)
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
	if len(reported) > 0 {
		d.forgetPRsOfHostsOtherThan(reported)
	}

	d.broadcastGitHubHosts(hostsBefore)
	return nil
}

func (d *Daemon) forgetPRsOfHostsOtherThan(hosts []string) {
	kept := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		kept[host] = true
	}
	d.life.Do("forgetPRsOfHostsOtherThan", func() {
		forgotten, err := d.store.ForgetPRsOfHostsOtherThan(kept)
		if err != nil {
			d.logf("Forgetting pull requests of signed-out GitHub hosts: %v", err)
			return
		}
		d.coalesceSnapshots(func() {
			for _, id := range forgotten {
				d.publishFact(FactPRDisappeared, id, nil)
			}
		})
	})
}

func (d *Daemon) registerGitHubClient(host string, client *github.Client) {
	d.store.RememberGitHubHost(host)
	d.ghRegistry.Register(host, client)
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

	client, err := github.NewClientForHost(d.life.Context(), host, mockURL, token)
	if err != nil {
		return err
	}

	for _, existing := range d.ghRegistry.Hosts() {
		d.ghRegistry.Remove(existing)
	}
	d.registerGitHubClient(host, client)
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

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		namedPID := "unknown"
		if data, readErr := os.ReadFile(d.pidPath); readErr == nil {
			if pid := strings.TrimSpace(string(data)); pid != "" {
				namedPID = pid
			}
		}
		if !namesGoneProcess(namedPID) {
			f.Close()
			return fmt.Errorf("%w (pid %s)", ErrAlreadyRunning, namedPID)
		}
		if err := d.awaitPIDLockRelease(f); err != nil {
			return fmt.Errorf("%w: pid %s is gone, but something else held %s for %s: %v", ErrAlreadyRunning, namedPID, d.pidPath, PIDLockReleaseTripwire, err)
		}
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

func namesGoneProcess(namedPID string) bool {
	pid, err := strconv.Atoi(namedPID)
	return err == nil && pid > 0 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func (d *Daemon) awaitPIDLockRelease(f *os.File) error {
	began := time.Now()
	released := make(chan error, 1)
	d.life.Go("awaitPIDLockRelease", func() {
		for {
			err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
			if !errors.Is(err, syscall.EINTR) {
				released <- err
				return
			}
		}
	})
	deadline := stepclock.NewTimer(PIDLockReleaseTimer, PIDLockReleaseTripwire)
	defer deadline.Stop()
	select {
	case err := <-released:
		d.logf("PID lock came free %s after its daemon was gone", time.Since(began))
		return err
	case <-deadline.C():
		return errors.New("the lock never came free")
	}
}

func (d *Daemon) releasePIDLock() {
	if d.pidFile != nil {
		syscall.Flock(int(d.pidFile.Fd()), syscall.LOCK_UN)
		d.pidFile.Close()
		d.pidFile = nil
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	d.handleConnectionContext(d.life.Context(), conn)
}

func (d *Daemon) handleConnectionContext(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	// Stop ends reads on a command socket at once and gives writes the 10s a WebSocket client gets (defaultWSWriteTimeout),
	// so a finished command still sends its reply but a client that stopped reading cannot hold stop.
	stopReading := context.AfterFunc(d.life.Context(), func() {
		_ = conn.SetReadDeadline(time.Now())
		_ = conn.SetWriteDeadline(time.Now().Add(defaultWSWriteTimeout))
	})
	defer stopReading()

	reader := bufio.NewReader(conn)
	data, err := readInitialSocketFrame(reader, protocol.MaxSocketFrameBytes)
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

	release, held := d.life.Hold("handleConnection")
	if !held {
		d.sendError(conn, errDaemonStopping.Error())
		return
	}
	defer release()
	cmd, msg, err := protocol.ParseMessage(data)
	if origin, remote := OriginOf(ctx); remote && !d.isOutpostConn(origin.Host, origin.bridge) {
		d.sendError(conn, "outpost bridge was replaced")
		return
	}
	if err == nil {
		place, declared := protocol.SocketPlacement(cmd, data)
		if !declared {
			d.sendError(conn, fmt.Sprintf("%s declares no placement", cmd))
			return
		}
		homeSetting := false
		if place.Locator == protocol.LocatorSetting {
			set, ok := msg.(*protocol.SetSettingMessage)
			if !ok {
				d.sendError(conn, "setting locator requires a setting key")
				return
			}
			spec, err := d.writableSettingHere(set.Key)
			if err != nil {
				d.sendError(conn, err.Error())
				return
			}
			homeSetting = spec.Class == setting.Home
		}
		if place.Locator == protocol.LocatorHome || homeSetting {
			status := d.enrollmentStatus()
			if !status.IsHome() {
				if _, asksStatus := msg.(*protocol.UpdateStatusMessage); asksStatus && d.answerUpdateStatusFromNotice(conn) {
					return
				}
				d.router().Forward(bufferedSocket{Conn: conn, reader: reader}, data, d.callerSession(ctx, data))
				return
			}
		}
	}
	caller := d.callerSession(ctx, data)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	if origin, remote := OriginOf(ctx); remote {
		switch cmd {
		case protocol.CmdOpenBrowser, protocol.CmdBrowserControl:
			d.sendError(conn, fmt.Sprintf("%s is not supported from %s", cmd, origin.Name))
			return
		case protocol.CmdOpenSentFiles:
			if msg.(*protocol.OpenSentFilesMessage).TerminalID == nil {
				d.sendError(conn, fmt.Sprintf("open_sent_files is not supported from %s", origin.Name))
				return
			}
		}
	}
	if err := d.scopeGardenRequest(cmd, msg, ""); err != nil {
		d.sendRefusal(conn, err.Error(), err)
		return
	}

	switch cmd {
	case protocol.CmdRecordAnalytics:
		d.handleRecordAnalytics(msg.(*protocol.RecordAnalyticsMessage), caller)
	case protocol.CmdGetSettings:
		d.handleGetSettings(conn, msg.(*protocol.GetSettingsMessage))
	case protocol.CmdSetSetting:
		d.handleSetSetting(conn, msg.(*protocol.SetSettingMessage))
	case protocol.CmdLaunchDesktopGet, protocol.CmdLaunchDesktopSet:
		d.handleLaunchDesktopCommand(conn, cmd, msg)
	case protocol.CmdDelegate:
		d.handleDelegate(ctx, conn, msg.(*protocol.DelegateMessage))
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

	case protocol.CmdAskOpen:
		d.handleAskOpen(conn, msg.(*protocol.AskOpenMessage))
	case protocol.CmdAskList:
		d.handleAskList(conn, msg.(*protocol.AskListMessage))
	case protocol.CmdAskClose:
		d.handleAskClose(conn, msg.(*protocol.AskCloseMessage))
	case protocol.CmdRecordVent:
		d.handleRecordVent(conn, msg.(*protocol.RecordVentMessage))
	case protocol.CmdWithdrawVent:
		d.handleWithdrawVent(conn, msg.(*protocol.WithdrawVentMessage))

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
		d.handleObserveAgentConversation(ctx, conn, msg.(*protocol.SetSessionResumeIDMessage))
	case protocol.CmdSessionInstructions:
		d.handleSessionInstructions(conn, msg.(*protocol.SessionInstructionsMessage))
	case protocol.CmdSessionTranscript:
		d.handleSessionTranscript(conn, msg.(*protocol.SessionTranscriptMessage))
	case protocol.CmdSessionList:
		d.handleSessionList(conn, msg.(*protocol.SessionListMessage))
	case protocol.CmdSessionShow:
		d.handleSessionShow(conn, msg.(*protocol.SessionShowMessage))
	case protocol.CmdUpdateStatus:
		d.handleUpdateStatus(ctx, conn)
	case protocol.CmdUpdateRequest:
		d.handleUpdateRequest(conn, msg.(*protocol.UpdateRequestMessage))
	case protocol.CmdStartUpdate, protocol.CmdDeclineUpdate:
		d.sendError(conn, "only the attn app can start or decline an update; run `attn update request` to ask the user")
	case protocol.CmdSessionReopen:
		d.handleSessionReopen(conn, msg.(*protocol.SessionReopenMessage))
	case protocol.CmdTerminalList:
		d.handleTerminalList(conn, msg.(*protocol.TerminalListMessage))
	case protocol.CmdTerminalOpen:
		d.handleTerminalOpen(conn, msg.(*protocol.TerminalOpenMessage))
	case protocol.CmdCodexStatus:
		d.handleCodexStatus(conn, msg.(*protocol.CodexStatusMessage))
	case protocol.CmdRestartSharedCodex:
		d.handleRestartSharedCodexCLI(conn, msg.(*protocol.RestartSharedCodexMessage))
	case protocol.CmdTerminalInput:
		d.handleTerminalInput(conn, msg.(*protocol.TerminalInputMessage))
	case protocol.CmdTerminalRead:
		d.handleTerminalRead(conn, msg.(*protocol.TerminalReadMessage))
	case protocol.CmdTerminalWait:
		d.handleTerminalWait(conn, msg.(*protocol.TerminalWaitMessage))
	case protocol.CmdTerminalClose:
		d.handleTerminalClose(conn, msg.(*protocol.TerminalCloseMessage))
	case protocol.CmdTerminalMove:
		d.handleTerminalMove(conn, msg.(*protocol.TerminalMoveMessage))
	case protocol.CmdDropOutpostCopy:
		if err := d.dropStaleCopy(); err != nil {
			d.sendError(conn, err.Error())
		} else {
			d.sendOK(conn)
		}
	case protocol.CmdSetSessionPriority:
		priority := msg.(*protocol.SetSessionPriorityMessage)
		if err := d.setSessionPriority(priority); err != nil {
			d.sendError(conn, err.Error())
		} else {
			d.sendOK(conn)
		}
	case protocol.CmdSnoozeTurn:
		d.handleSnoozeTurnConn(conn, msg.(*protocol.SnoozeTurnMessage))
	case protocol.CmdAgentTurn:
		d.handleAgentTurn(conn, msg.(*protocol.AgentTurnMessage))
	case protocol.CmdWakeTurn:
		d.handleWakeTurnConn(conn, msg.(*protocol.WakeTurnMessage))
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
	case protocol.CmdAgentReload:
		d.handleAgentReload(conn, msg.(*protocol.AgentReloadMessage))
	case protocol.CmdQuickCaptureSend, protocol.CmdQuickCaptureAttachmentPut, protocol.CmdQuickCaptureAttachmentGet, protocol.CmdQuickCaptureAttachmentDiscard:
		d.handleQuickCapture(conn, msg)
	case protocol.CmdSenderPair:
		d.handleSenderPair(conn, msg.(*protocol.SenderPairMessage), caller)
	case protocol.CmdSenderList:
		d.handleSenderList(conn, msg.(*protocol.SenderListMessage), caller)
	case protocol.CmdSenderUnpair:
		d.handleSenderUnpair(conn, msg.(*protocol.SenderUnpairMessage), caller)
	case protocol.CmdSenderSessions:
		d.handleSenderSessions(conn, msg.(*protocol.SenderSessionsMessage))
	case protocol.CmdQuickCaptureGet, protocol.CmdQuickCaptureList:
		d.sendError(conn, "quick capture history is app-only; use the authenticated app WebSocket channel")
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
		d.handleSeedShow(ctx, conn, msg.(*protocol.SeedShowMessage))
	case protocol.CmdSeedArtifactTransfer:
		d.handleSeedArtifactTransfer(ctx, conn, msg.(*protocol.SeedArtifactTransferMessage))
	case protocol.CmdSeedEdit:
		d.handleSeedEdit(conn, msg.(*protocol.SeedEditMessage))
	case protocol.CmdSeedRollback:
		d.handleSeedRollback(conn, msg.(*protocol.SeedRollbackMessage))
	case protocol.CmdSeedAuthorization:
		d.handleSeedAuthorization(conn, msg.(*protocol.SeedAuthorizationMessage))
	case protocol.CmdSeedReminder:
		d.handleSeedReminder(conn, msg.(*protocol.SeedReminderMessage))
	case protocol.CmdSeedStandby:
		d.handleSeedStandby(conn, msg.(*protocol.SeedStandbyMessage))
	case protocol.CmdSeedTransition:
		d.handleSeedTransition(conn, msg.(*protocol.SeedTransitionMessage))
	case protocol.CmdSeedNote:
		d.handleSeedNote(conn, msg.(*protocol.SeedNoteMessage))
	case protocol.CmdSeedCheck:
		d.handleSeedCheck(ctx, conn, msg.(*protocol.SeedCheckMessage))
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
	case protocol.CmdCrewStart:
		d.handleCrewStart(conn, msg.(*protocol.CrewStartMessage))
	case protocol.CmdCrewClose:
		d.handleCrewClose(conn, msg.(*protocol.CrewCloseMessage))
	case protocol.CmdCrewApply:
		d.handleCrewApply(conn, msg.(*protocol.CrewApplyMessage))
	case protocol.CmdCrewRename:
		d.handleCrewRename(conn, msg.(*protocol.CrewRenameMessage))
	case protocol.CmdCrewCreate:
		d.handleCrewCreate(conn, msg.(*protocol.CrewCreateMessage))
	case protocol.CmdCrewRetire:
		d.handleCrewRetire(conn, msg.(*protocol.CrewRetireMessage))
	case protocol.CmdCrewRestore:
		d.handleCrewRestore(conn, msg.(*protocol.CrewRestoreMessage))
	case protocol.CmdCrewMoveHome:
		d.handleCrewMoveHome(conn, msg.(*protocol.CrewMoveHomeMessage))
	case protocol.CmdCrewSet:
		d.handleCrewSet(conn, msg.(*protocol.CrewSetMessage))
	case protocol.CmdCrewPrime:
		d.handleCrewPrime(conn, msg.(*protocol.CrewPrimeMessage))
	case protocol.CmdCrewHandoff:
		d.handleCrewHandoff(conn, msg.(*protocol.CrewHandoffMessage))
	case protocol.CmdOnboarding:
		d.handleOnboarding(conn, msg.(*protocol.OnboardingMessage))
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
	case protocol.CmdPullRequestWatchList:
		d.handlePullRequestWatchList(conn, msg.(*protocol.PullRequestWatchListMessage))
	case protocol.CmdQuery:
		d.handleQuery(conn, msg.(*protocol.QueryMessage))
	case protocol.CmdCallerSession:
		d.handleCallerSession(conn, caller)
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
		d.handleOpenMarkdown(ctx, conn, msg.(*protocol.OpenMarkdownMessage))
	case protocol.CmdReviewStart:
		d.handleReviewStart(ctx, conn, msg.(*protocol.ReviewStartMessage))
	case protocol.CmdReviewCheck:
		d.handleReviewCheck(ctx, conn, msg.(*protocol.ReviewCheckMessage))
	case protocol.CmdReviewAnswer:
		d.handleReviewAnswer(ctx, conn, msg.(*protocol.ReviewAnswerMessage))
	case protocol.CmdOpenSeed:
		d.handleOpenSeed(conn, msg.(*protocol.OpenSeedMessage))
	case protocol.CmdOpenSentFiles:
		d.handleOpenSentFiles(conn, msg.(*protocol.OpenSentFilesMessage))
	case protocol.CmdOpenBrowser:
		d.handleOpenBrowser(conn, msg.(*protocol.OpenBrowserMessage))
	case protocol.CmdCalloutShow:
		d.handleCalloutShow(conn, msg.(*protocol.CalloutShowMessage))
	case protocol.CmdCalloutAnchors:
		d.handleCalloutAnchors(conn, msg.(*protocol.CalloutAnchorsMessage))
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

func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	teardown, err := d.prepareSessionTeardown(msg.ID)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("prepare session teardown: %v", err))
		return
	}
	if err := d.commitSessionUnregister(msg.ID, store.SessionClose{By: who.User()}); err != nil {
		d.cancelSessionTeardown(msg.ID, teardown)
		d.sendError(conn, fmt.Sprintf("close session: %v", err))
		return
	}
	d.sendOK(conn)

	if teardown != nil {
		d.terminateSessionAsync(msg.ID, syscall.SIGTERM, teardown)
	}
}

func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	monitor := d.monitorInTerminal(msg.ID, sessionID)
	d.logf("hook evidence: id=%s state=%s", sessionID, msg.State)
	d.tracePermissionMode(sessionID, protocol.Deref(msg.PermissionMode))
	d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: sessionmonitor.StateSourceHook}, msg.State)
	monitor.RecordBracket(msg.State)
	if strings.EqualFold(strings.TrimSpace(protocol.Deref(msg.HookEvent)), "user_prompt_submit") &&
		strings.TrimSpace(protocol.Deref(msg.Prompt)) != "" {
		typed := monitor.ObservePromptSubmitted(time.Now())
		prompt := protocol.Deref(msg.Prompt)
		d.life.Go("maybeGenerateSessionTitleFromPrompt", func() { monitor.TitleFromPrompt(prompt, typed) })
	}
	d.touchSession(sessionID)
	d.sendOK(conn)
}

func (d *Daemon) persistResumeSessionID(sessionID protocol.SessionID, resumeSessionID string) (*sessionmonitor.ConversationReceipt, error) {
	if monitor := d.sessionMonitor(sessionID); monitor != nil {
		receipt, accepted := monitor.ObserveResumeID(resumeSessionID)
		if !accepted {
			return nil, errors.New("resume conversation could not be saved")
		}
		return receipt, nil
	}
	if _, err := d.store.TransitionSessionResumeID(sessionID, resumeSessionID); err != nil {
		return nil, err
	}
	return nil, nil
}

func (d *Daemon) handleStop(conn net.Conn, msg *protocol.StopMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	monitor := d.monitorInTerminal(msg.ID, sessionID)
	reportedTranscriptPath := strings.TrimSpace(msg.TranscriptPath)
	msg.TranscriptPath = d.resolveStopTranscriptPath(d.store.Get(sessionID), reportedTranscriptPath)
	if monitor != d.sessionMonitor(sessionID) {
		msg.TranscriptPath = monitor.Snapshot().Conversation.V.TranscriptPath
	}
	if reportedTranscriptPath != "" && msg.TranscriptPath != reportedTranscriptPath {
		d.logf("handleStop: ignored transcript path for session=%s: reported=%s bound=%s", sessionID, reportedTranscriptPath, msg.TranscriptPath)
	}
	d.logf("handleStop: session=%s, transcript_path=%s", sessionID, msg.TranscriptPath)

	relaxBackgroundWork := monitor.Policy().ChiefOfStaff
	classifies := !monitor.ConsumeForcedStop()
	if classifies {
		monitor.CancelAutoSettle("stop judged")
	}
	monitor.RecordStopFacts(!relaxBackgroundWork && hasActiveBackgroundTask(msg), hasPendingSessionCron(msg))
	if stopIsNonTerminal(msg, relaxBackgroundWork) {
		tasks := describeBackgroundTasks(msg)
		d.logf(
			"handleStop: non-terminal stop session=%s pending_crons=%d background_tasks=[%s]",
			sessionID, protocol.Deref(msg.PendingSessionCrons), tasks,
		)
		d.traceStateEvidence(
			sessionID,
			sessionmonitor.Origin{Source: sessionmonitor.StateSourceStopHook, Detail: "non-terminal stop: " + tasks},
			"",
		)
		d.sendOK(conn)
		if !classifies {
			d.logf("handleStop: skipping yield classification for daemon-terminated session=%s", sessionID)
			return
		}
		classification := sessionmonitor.StopClassification{
			Yielded:                true,
			RunningBackgroundTasks: runningBackgroundTaskCount(msg),
		}
		d.life.Go("classifyStop", func() { monitor.ClassifyStop(msg.TranscriptPath, classification) })
		return
	}

	monitor.RecordTurnEnded(classifies)
	if monitor.Session() != "" {
		monitor.EnsureTranscriptWatcher(msg.TranscriptPath)
	}

	if monitor != nil {
		target, _ := monitor.TitleTarget()
		driver := agentdriver.Get(string(target.Agent))
		native := agentdriver.ResumeSessionIDFromTranscriptPath(driver, msg.TranscriptPath)
		if native != "" {
			proposal := sessionmonitor.ConversationObserved
			if !agentdriver.EffectiveCapabilities(driver).HasHooks {
				proposal = sessionmonitor.ConversationReported
			}
			monitor.ProposeConversation(sessionmonitor.ConversationObservation{NativeID: native, TranscriptPath: msg.TranscriptPath}, proposal, "", "")
		}
	}
	monitor.Touch(time.Now())
	d.sendOK(conn)

	if !classifies {
		d.logf("handleStop: skipping classification for daemon-terminated session=%s", sessionID)
		return
	}

	d.life.Go("classifySessionState", func() { monitor.ClassifyStop(msg.TranscriptPath, sessionmonitor.StopClassification{}) })
	d.life.Go("maybeGenerateSessionTitle", func() { monitor.TitleFromTranscript(msg.TranscriptPath) })
}

func (d *Daemon) observeStoppedConversation(sessionID protocol.SessionID, transcriptPath string) (*sessionmonitor.ConversationReceipt, bool) {
	if session := d.store.Get(sessionID); session != nil {
		driver := agentdriver.Get(session.Agent)
		resumeSessionID := agentdriver.ResumeSessionIDFromTranscriptPath(driver, transcriptPath)
		observation := agentConversationObservation{SessionID: sessionID, NativeID: resumeSessionID, TranscriptPath: transcriptPath}
		switch {
		case resumeSessionID == "":
		case agentdriver.EffectiveCapabilities(driver).HasHooks:
			return d.proposeMonitorConversation(observation, sessionmonitor.ConversationObserved, "")
		default:
			return d.claimAgentConversation(observation)
		}
	}
	return nil, false
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

	if watched := d.sessionMonitor(session.ID).LiveTranscriptPath(session.Agent); watched != "" {
		return watched
	}

	return path
}

func cloneSession(session *protocol.Session) *protocol.Session {
	if session == nil {
		return nil
	}
	clone := *session
	return &clone
}

func (d *Daemon) sessionForBroadcast(session *protocol.Session) *protocol.Session {
	dispatchers := d.gardenDispatchersBySession()
	var parties parties
	if session != nil {
		if _, dispatched := dispatchers[session.ID]; dispatched {
			parties = d.partiesNow()
		}
	}
	decorated := d.sessionForBroadcastWithChiefOfStaff(
		session,
		d.profileChiefs(),
		d.delegatedFromChiefSessionIDs(),
		d.crewMembersBySession(),
		d.gardenDispatchSeedsBySession(),
		dispatchers,
		parties,
	)
	if decorated != nil {
		decorated.DelegationRole = d.sessionDelegationRoles()[decorated.ID]
		decorated.Automation = d.automationProvenanceForSession(decorated.ID)
		decorated.PullRequests = d.sessionPullRequestsForSession(decorated)
		decorated.ConversationSharedWith = d.conversationsSharedByLiveSessions()[decorated.ID]
	}
	return decorated
}

func (d *Daemon) sessionForBroadcastWithChiefOfStaff(
	session *protocol.Session,
	chiefs map[string]protocol.SessionID,
	delegatedFromChief map[protocol.SessionID]bool,
	crewBySession map[protocol.SessionID]crew.Member,
	seedBySession map[protocol.SessionID]string,
	dispatcherBySession map[protocol.SessionID]who.Actor,
	parties parties,
) *protocol.Session {
	clone := cloneSession(session)
	if clone == nil {
		return nil
	}
	if d.store != nil {
		clone.TerminalExit = d.sessionExit(clone.ID)
	}
	d.decorateSessionWithStateReason(clone)

	d.decorateSessionWithAutoSettle(clone)
	d.decorateSessionWithSnooze(clone)
	d.decorateChiefOfStaff(clone, chiefs)
	d.decorateDelegatedFromChief(clone, delegatedFromChief)
	d.decorateCrewMember(clone, crewBySession)
	d.decorateSessionSeed(clone, seedBySession)
	d.decorateSessionDispatcher(clone, dispatcherBySession, parties)
	if seedBySession[clone.ID] != "" && clone.SeedID == nil {
		clone.Dispatcher, clone.DispatcherSessionID = nil, nil
		clone.DelegatedFromChief = nil
	}
	d.decorateSessionWithCost(clone)
	d.decorateSessionWithTitle(clone)
	d.decorateSessionWithTerminalBuild(clone)
	d.decorateSessionWithTurn(clone)
	d.decorateSessionHidden(clone)
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
	var parties parties
	if len(dispatcherBySession) > 0 {
		parties = d.partiesNow()
	}
	rolesBySession := d.sessionDelegationRoles()
	bySession := d.latestAutomationProvenance()
	pullRequestWatchesByPR := d.pullRequestWatchesByPR()
	ids := make([]protocol.SessionID, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	pullRequestsBySession := d.store.SessionPullRequestsOf(withWatchHolders(ids, pullRequestWatchesByPR))
	sharedConversations := d.conversationsSharedByLiveSessions()
	out := make([]protocol.Session, 0, len(sessions))
	for _, session := range sessions {
		if decorated := d.sessionForBroadcastWithChiefOfStaff(session, chiefs, delegatedFromChief, crewBySession, seedBySession, dispatcherBySession, parties); decorated != nil {
			decorated.DelegationRole = rolesBySession[decorated.ID]
			decorated.Automation = bySession[decorated.ID]
			decorated.ConversationSharedWith = sharedConversations[decorated.ID]
			addresses := []who.Address{who.ToSession(decorated.ID)}
			if member, bound := crewBySession[decorated.ID]; bound {
				addresses = append(addresses, who.Member(member.Key).Address())
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
	var paths []string
	for _, path := range msg.Paths {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".markdown":
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		d.sendOK(conn)
		return
	}
	now := time.Now()
	if monitor := d.sessionMonitor(sessionID); monitor != nil {
		if !monitor.ReportEditedFiles(paths, now) {
			d.sendError(conn, "edited file monitor view was not applied")
			return
		}
	} else {
		for _, path := range paths {
			d.store.RecordFileActivity(store.FileActivity{Path: path, Source: store.FileActivitySourceEdited, SessionID: sessionID, At: now})
		}
	}
	d.sendOK(conn)
}

func (d *Daemon) handleCallerSession(conn net.Conn, caller protocol.SessionID) {
	resp := protocol.Response{Ok: true}
	if caller != "" {
		resp.CallerSessionID = protocol.Ptr(caller)
	}
	json.NewEncoder(conn).Encode(resp)
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
	json.NewEncoder(conn).Encode(resp)
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

	host, repo, number, err := protocol.ParsePRID(id)
	if err != nil {
		return nil, err
	}

	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, fmt.Errorf("no client for host %s", host)
	}

	changed, err := d.fetchPRDetails(client, id, repo, number)
	if err != nil {
		return nil, fmt.Errorf("fetch details for %s: %w", id, err)
	}
	if changed {
		d.publishFact(FactPRDetailsChanged, id, nil)
	}

	return d.store.ListPRsByRepoHost(repo, host), nil
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

func (d *Daemon) sendRefusal(conn net.Conn, message string, err error) {
	json.NewEncoder(conn).Encode(protocol.Response{Ok: false, Error: protocol.Ptr(message), Refusal: protocol.RefusalOf(err)})
}

type successfulPRObservation struct {
	prs        []*protocol.PR
	observedAt time.Time
}

func (d *Daemon) pollPRs() {
	d.log("PR polling loop started (90s interval)")

	d.doPRPoll()
	d.every(prPollTimer, 90*time.Second, d.doPRPoll)
}

const prPollTimer stepclock.Name = "daemon-pr-poll"

func (d *Daemon) doPRPoll() {
	if !d.githubAvailable() {
		return
	}
	d.prRefreshMu.Lock()

	observedByHost := make(map[string]successfulPRObservation)
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
				continue
			}
			d.logf("PR poll error for %s: %v", host, err)
			continue
		}

		observedByHost[host] = successfulPRObservation{prs: prs, observedAt: observedAt}
	}

	if !earliestReset.IsZero() {
		d.broadcastRateLimited("search", earliestReset)
	}

	stored := d.life.Do("doPRPoll", func() { d.storePRPoll(observedByHost) })
	d.prRefreshMu.Unlock()
	if stored {
		d.doDetailRefresh()
	}
}

func (d *Daemon) saveObservedPRs(observedByHost map[string]successfulPRObservation) ([]*protocol.PR, error) {
	prsByHost := make(map[string][]*protocol.PR, len(observedByHost))
	for host, observation := range observedByHost {
		prsByHost[host] = observation.prs
	}
	pausepoint.At(pausepoint.PRPollSaving)
	changes, err := d.store.ReplacePRsOfHosts(prsByHost)
	if err != nil {
		return nil, err
	}
	d.publishPRChanges(changes)
	return d.store.ListPRs(""), nil
}

func (d *Daemon) storePRPoll(observedByHost map[string]successfulPRObservation) {
	if currentPRs, err := d.saveObservedPRs(observedByHost); err != nil {
		d.logf("PR poll: saving results: %v", err)
	} else {
		waiting := 0
		for _, pr := range currentPRs {
			if pr.State == protocol.PRStateWaiting && !pr.Muted {
				waiting++
			}
		}
		d.logf("PR poll: %d PRs (%d waiting)", len(currentPRs), waiting)
	}

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

	var changedIDs []string
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
			continue
		}

		if limited, resetAt := client.IsRateLimited("core"); limited {
			d.logf("Detail refresh: %s rate limited until %v", host, resetAt)
			limitedHosts[host] = resetAt
			continue
		}

		changed, err := d.fetchPRDetails(client, pr.ID, pr.Repo, pr.Number)
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("core"); info != nil {
					d.logf("Detail refresh: %s rate limited, stopping host refresh", host)
					limitedHosts[host] = info.ResetAt
				}
				continue
			}
			d.logf("Failed to refresh details for %s: %v", pr.ID, err)
			continue
		}
		if changed {
			changedIDs = append(changedIDs, pr.ID)
		}
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

	d.publishPRDetailsChanged("Detail refresh", changedIDs)
}

func (d *Daemon) publishPRDetailsChanged(origin string, ids []string) {
	if len(ids) == 0 {
		return
	}
	d.logf("%s: details changed on %d PRs", origin, len(ids))
	d.coalesceSnapshots(func() {
		for _, id := range ids {
			d.publishFact(FactPRDetailsChanged, id, nil)
		}
	})
}

func (d *Daemon) fetchAllPRDetails() (askedEveryPR bool) {
	if !d.githubAvailable() {
		return false
	}

	allPRs := d.store.ListPRs("")
	if len(allPRs) == 0 {
		return false
	}

	d.logf("App launch: fetching details for %d PRs", len(allPRs))

	var changedIDs []string
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

		changed, err := d.fetchPRDetails(client, pr.ID, pr.Repo, pr.Number)
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("core"); info != nil {
					d.logf("App launch: %s rate limited, stopping host fetch loop", host)
					limitedHosts[host] = info.ResetAt
				}
				continue
			}
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}
		if changed {
			changedIDs = append(changedIDs, pr.ID)
		}
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

	d.publishPRDetailsChanged("App launch", changedIDs)
	return len(limitedHosts) == 0
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

	msg.Session.Agent = normalizeSpawnAgent(msg.Session.Agent)
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
	if member := strings.TrimSpace(protocol.Deref(msg.Session.CrewMember)); member != "" {
		d.recordMemberSession(member, msg.Session.ID)
	}
	if !protocol.Deref(msg.Unplaced) {
		d.placeLaunchedSession(&msg.Session, &launchPlacement{direction: layouttree.DirectionVertical, focus: true})
	}
	d.startSessionMonitorSeeded(&msg.Session, sessionmonitor.Evidence{})
	d.publishFact(FactSessionRegistered, string(msg.Session.ID), nil)
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

	observedByHost := make(map[string]successfulPRObservation)
	var firstErr error

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
			continue
		}
		observedByHost[host] = successfulPRObservation{prs: prs, observedAt: observedAt}
	}

	var currentPRs []*protocol.PR
	var saveErr error
	if !d.life.Do("doRefreshPRs", func() {
		currentPRs, saveErr = d.saveObservedPRs(observedByHost)
	}) {
		return errDaemonStopping
	}

	if saveErr == nil {
		d.logf("PR refresh: %d PRs fetched", len(currentPRs))
	}
	for host, observation := range observedByHost {
		d.observeGitHubReviewRequests(host, observation.prs, observation.observedAt)
	}
	if saveErr != nil {
		return fmt.Errorf("saving PR refresh: %w", saveErr)
	}

	if len(observedByHost) == 0 && firstErr != nil {
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

	changed, err := d.fetchPRDetails(client, prID, pr.Repo, pr.Number)
	if err != nil {
		if errors.Is(err, github.ErrRateLimited) {
			d.logf("Immediate fetch for %s: rate limited", prID)
			if info := client.GetRateLimit("core"); info != nil {
				d.broadcastRateLimited("core", info.ResetAt)
			}
			return
		}
		d.logf("Immediate fetch failed for %s: %v", prID, err)
		return
	}

	if changed {
		d.publishFact(FactPRDetailsChanged, prID, nil)
	}
	d.logf("Immediate fetch complete for %s (heat=hot)", prID)
}

func (d *Daemon) monitorBranches() {
	d.logf("Branch monitoring started (%s interval)", branchMonitorInterval)

	check := func() { d.life.Do("monitorBranches", d.checkAllBranches) }
	check()
	d.every(branchMonitorTimer, branchMonitorInterval, check)
}

const branchMonitorTimer stepclock.Name = "daemon-branch-monitor"

func (d *Daemon) checkAllBranches() {
	d.coalesceSnapshots(func() {
		for _, monitor := range d.monitors().each() {
			monitor.CheckBranch()
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

func (d *Daemon) projectSessionsUpdated() {
	d.projectSnapshot(snapshotSessions, func() {
		if d.wsHub == nil || d.store == nil {
			return
		}
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: d.sessionsForBroadcast(d.store.List("")),
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
