package pausepoint

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	EnvPoints = "ATTN_PAUSE_AT"
	EnvSocket = "ATTN_PAUSE_SOCK"
	Release   = "go"
	Failed    = "failed"
	Every     = "*"
)

const (
	AnalyticsForwarded        = "analytics-forwarded"
	AnalyticsForwardRead      = "analytics-forward-read"
	AnalyticsForwardImporting = "analytics-forward-importing"
	PtyAttachSnapshot         = "pty-attach-snapshot"
	PtyOutputPending          = "pty-output-pending"
	PtyOutputHeld             = "pty-output-held"
	PtyStreamRead             = "pty-stream-read"
	PtySubscriberDrop         = "pty-subscriber-drop"
	PtyWatchReplay            = "pty-watch-replay"
	PtyHostWatchAll           = "pty-host-watch-all"
	PtyWorkerUpgrade          = "pty-worker-upgrade"
	PtyWaitBlock              = "pty-wait-block"
	PtyRemoveReplied          = "pty-remove-replied"
	PtyWorkerStopped          = "pty-worker-stopped"
	BusAnnounce               = "bus-announce"
	BusEventHandled           = "bus-event-handled"
	BusProducersReading       = "bus-producers-reading"
	SeedRollbackRead          = "seed-rollback-read"
	SeedReminderDue           = "seed-reminder-due"
	SeedReminderRead          = "seed-reminder-read"

	DaemonEnrollmentLoaded     = "daemon-enrollment-loaded"
	DaemonStartupRecovery      = "daemon-startup-recovery"
	RecoverableAgentsReviving  = "recoverable-agents-reviving"
	AttachRevivePending        = "attach-revive-pending"
	LaunchAwaitsPluginHarness  = "launch-awaits-plugin-harness"
	DaemonInitialStateBuilt    = "daemon-initial-state-built"
	SessionInputPasteGap       = "session-input-paste-gap"
	ClaudeModReturnWaits       = "claude-mod-return-waits"
	RingPlacementAnswering     = "ring-placement-answering"
	TenderDirectoryInspecting  = "tender-directory-inspecting"
	SessionInputLaneContended  = "session-input-lane-contended"
	CompactingClosePlacing     = "compacting-close-placing"
	CompactingCloseFinishing   = "compacting-close-finishing"
	SessionSnoozeArrived       = "session-snooze-arrived"
	SessionSnoozeDecided       = "session-snooze-decided"
	SessionTeardownPrepared    = "session-teardown-prepared"
	SessionTeardownKilling     = "session-teardown-killing"
	AgentCloseDrained          = "agent-close-drained"
	SessionExitObserved        = "session-process-exit-observed"
	AgentTerminalStarted       = "agent-terminal-started"
	TerminalStarting           = "terminal-starting"
	AgentLaunchCommitReporting = "agent-launch-commit-reporting"
	CodexViewReleaseChecked    = "codex-view-release-checked"
	ConversationCandidateTurn  = "conversation-candidate-turn"
	ConversationDecision       = "conversation-decision"
	ConversationOwnerResolved  = "conversation-owner-resolved"
	ConversationObserved       = "conversation-observed"
	ConversationMoveSaved      = "conversation-move-saved"
	ConversationInputLookup    = "conversation-input-lookup"
	ConversationCandidateBound = "conversation-candidate-bound"
	ConversationCandidateStart = "conversation-candidate-start"
	MemberRetiredClearSleep    = "member-retired-clear-sleep"
	ConversationDropped        = "conversation-dropped"
	ConversationReportReady    = "conversation-report-ready"
	ConversationApplyAwaited   = "conversation-apply-awaited"
	OutpostConversationApplied = "outpost-conversation-applied"
	PtyExitOwnerResolved       = "pty-exit-owner-resolved"
	PtyExitHandled             = "pty-exit-handled"
	CodexServerExitHandled     = "codex-server-exit-handled"
	TerminalExitReconciled     = "terminal-exit-reconciled"
	CopyBatchStaged            = "copy-batch-staged"
	CopyBatchApplied           = "copy-batch-applied"
	OutboxSessionRead          = "outbox-session-read"
	OutboxSessionApplied       = "outbox-session-applied"
	SessionLinkSetting         = "session-link-setting"
	TerminalCopyApplied        = "terminal-copy-applied"
	NotebookStreamOpen         = "notebook-stream-open"
	NotebookReportSent         = "notebook-report-sent"
	OutpostBridgeAccept        = "outpost-bridge-accept"
	NotebookStreamError        = "notebook-stream-error"
	NotebookStreamErrorHandled = "notebook-stream-error-handled"
	BridgeConnectionClosed     = "bridge-connection-closed"
	BridgeChannelOpening       = "bridge-channel-opening"
	RemoteInputAnswered        = "remote-input-answered"
	BridgeSendQueued           = "bridge-send-queued"
	OutpostMachineRead         = "outpost-machine-read"
	ClassifierVerdictReached   = "classifier-verdict-reached"
	EnsureFoundNoListener      = "ensure-found-no-listener"
	OutpostRecoveryReported    = "outpost-recovery-reported"
	OutpostRecoveryStarted     = "outpost-recovery-started"
	OutpostRecoveryApplied     = "outpost-recovery-applied"
	ForwardSocketCreated       = "forward-socket-created"
	ProfileActionRan           = "profile-action-ran"
	DatabaseBackupStarting     = "database-backup-starting"
	DatabaseBackupCopied       = "database-backup-copied"
	AutomationRunReserved      = "automation-run-reserved"
	AutomationAgentStarted     = "automation-agent-started"
	AutomationRunFinalizing    = "automation-run-finalizing"
	RemoteRecentRecorded       = "remote-recent-recorded"
	KittyImageRequested        = "kitty-image-requested"
	PtySizeOwnerChecked        = "pty-size-owner-checked"
	PtySizesReleased           = "pty-sizes-released"
	HomeGatewayScreenCaptured  = "home-gateway-screen-captured"
)

type armed struct {
	socket string
	mu     sync.Mutex
	points map[string]bool
}

var load = sync.OnceValue(func() *armed {
	names, socket := os.Getenv(EnvPoints), os.Getenv(EnvSocket)
	if names == "" || socket == "" {
		return nil
	}
	a := &armed{socket: socket, points: map[string]bool{}}
	for _, name := range strings.Split(names, ",") {
		if name = strings.TrimSpace(name); name != "" {
			trimmed := strings.TrimSuffix(name, Every)
			a.points[trimmed] = trimmed != name
		}
	}
	return a
})

// Route holds this process at its armed points instead of the socket a stack test listens on.
type Route interface {
	Hold(name string, failed error)
	Armed() []string
}

var inProcess atomic.Pointer[Route]

// InProcess routes this process's At and Done calls to route until restore; it refuses a second route.
func InProcess(route Route) (restore func(), err error) {
	if !inProcess.CompareAndSwap(nil, &route) {
		return nil, fmt.Errorf("pausepoint: a route is already installed with points %v; one in-process route per process", (*inProcess.Load()).Armed())
	}
	return func() { inProcess.CompareAndSwap(&route, nil) }, nil
}

// At holds the process here when a test armed name.
func At(name string) {
	reach(name, nil)
}

// Done marks a finished step: it holds like At when err is nil, and otherwise reports err to the test without holding.
func Done(name string, err error) {
	reach(name, err)
}

func reach(name string, failed error) {
	if route := inProcess.Load(); route != nil {
		(*route).Hold(name, failed)
		return
	}
	if a := load(); a != nil {
		a.pause(name, failed)
	}
}

func (a *armed) pause(name string, failed error) {
	a.mu.Lock()
	every, armed := a.points[name]
	// Once per process unless armed for every hit: a worker that re-execs itself for an upgrade arms again.
	if !every {
		delete(a.points, name)
	}
	a.mu.Unlock()
	if !armed {
		return
	}
	conn, err := net.Dial("unix", a.socket)
	if err != nil {
		return
	}
	defer conn.Close()
	if failed != nil {
		_, _ = fmt.Fprintf(conn, "%s %d %s %s\n", name, os.Getpid(), Failed, strconv.Quote(failed.Error()))
		return
	}
	if _, err := fmt.Fprintf(conn, "%s %d\n", name, os.Getpid()); err != nil {
		return
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == Release {
			return
		}
	}
}
