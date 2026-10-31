package pausepoint

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

const (
	EnvPoints = "ATTN_PAUSE_AT"
	EnvSocket = "ATTN_PAUSE_SOCK"
	Release   = "go"
	Every     = "*"
)

const (
	PtyAttachSnapshot = "pty-attach-snapshot"
	PtyOutputPending  = "pty-output-pending"
	PtyOutputHeld     = "pty-output-held"
	PtyStreamRead     = "pty-stream-read"
	PtySubscriberDrop = "pty-subscriber-drop"
	PtyWatchReplay    = "pty-watch-replay"
	PtyWorkerUpgrade  = "pty-worker-upgrade"
	PtyWaitBlock      = "pty-wait-block"
	PtyRemoveReplied  = "pty-remove-replied"
	BusAnnounce       = "bus-announce"
	BusEventHandled   = "bus-event-handled"
	SeedRollbackRead  = "seed-rollback-read"

	DaemonEnrollmentLoaded     = "daemon-enrollment-loaded"
	DaemonStartupRecovery      = "daemon-startup-recovery"
	SessionInputPasteGap       = "session-input-paste-gap"
	SessionInputLaneContended  = "session-input-lane-contended"
	SessionSnoozeCommitted     = "session-snooze-committed"
	SessionStateReacting       = "session-state-reacting"
	SessionTeardownPrepared    = "session-teardown-prepared"
	AgentCloseDrained          = "agent-close-drained"
	SessionExitObserved        = "session-process-exit-observed"
	AgentTerminalStarted       = "agent-terminal-started"
	TerminalStarting           = "terminal-starting"
	AgentLaunchReady           = "agent-launch-ready"
	CodexViewReleaseChecked    = "codex-view-release-checked"
	ConversationCandidateTurn  = "conversation-candidate-turn"
	ConversationDecision       = "conversation-decision"
	ConversationOwnerResolved  = "conversation-owner-resolved"
	ConversationObserved       = "conversation-observed"
	ConversationOutcome        = "conversation-outcome"
	ConversationInputLookup    = "conversation-input-lookup"
	ConversationCandidateBound = "conversation-candidate-bound"
	ConversationCandidateStart = "conversation-candidate-start"
	MemberRetiredClearSleep    = "member-retired-clear-sleep"
	ConversationDropped        = "conversation-dropped"
	ConversationReportReady    = "conversation-report-ready"
	PtyExitOwnerResolved       = "pty-exit-owner-resolved"
	PtyExitHandled             = "pty-exit-handled"
	TerminalExitReconciled     = "terminal-exit-reconciled"
	DriverTranscriptCaptured   = "driver-transcript-captured"
	DriverTranscriptApplied    = "driver-transcript-applied"
	CopyBatchStaged            = "copy-batch-staged"
	CopyBatchApplied           = "copy-batch-applied"
	NotebookStreamOpen         = "notebook-stream-open"
	NotebookReportSent         = "notebook-report-sent"
	OutpostBridgeAccept        = "outpost-bridge-accept"
	NotebookStreamError        = "notebook-stream-error"
	NotebookStreamErrorHandled = "notebook-stream-error-handled"
	BridgeConnectionClosed     = "bridge-connection-closed"
	RemoteInputAnswered        = "remote-input-answered"
	OutpostRecoveryReported    = "outpost-recovery-reported"
	OutpostRecoveryStarted     = "outpost-recovery-started"
	OutpostRecoveryApplied     = "outpost-recovery-applied"
	ForwardSocketCreated       = "forward-socket-created"
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

func At(name string) {
	if a := load(); a != nil {
		a.pause(name)
	}
}

func (a *armed) pause(name string) {
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
