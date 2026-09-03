package ptybackend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/pty"
)

const (
	OutputEventKindOutput = "output"
	OutputEventKindDesync = "desync"
	OutputEventKindExit   = "exit"
	// Placement positions only mean anything in order against the same-seq bytes.
	OutputEventKindPlacements = "kitty_placements"
)

type SpawnOptions struct {
	ID    string
	CWD   string
	Agent string
	Label string

	Cols uint16
	Rows uint16

	ResumeSessionID   string
	ResumePicker      bool
	YoloMode          bool
	InitialPromptFile string

	Theme pty.TerminalTheme

	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	ExternalCommand   []string
	ExternalEnv       []string
	ExternalCWD       string
	// Every runtime reapplies this after login-shell and plugin environment.
	DaemonEnv   []string
	LifecycleID string

	// The host forks this file instead of appending, so two sessions can start
	// from the same one. PTY-backed agents resume through ResumeSessionID.
	ResumeConversationFile string

	// Skips the ~130ms readLoginShellEnv in workers.
	LoginShellEnv []string

	WorkflowGuidanceEnabled bool

	// Yolo overrides AutoApprove.
	AutoApprove bool
	// Empty only for legacy callers predating route recording.
	ApprovalRoute         launchcontract.ApprovalRoute
	TrustWorkingDirectory bool

	Model string

	Effort string

	// In tokens.
	ContextWindowCap int

	// When set, this is the sole source for agent, executable, approval, trust,
	// model, effort, and recovery policy across all paths.
	UnattendedLaunch launchcontract.UnattendedLaunchSpec
}

func validateUnattendedSpawnOptions(opts SpawnOptions) error {
	if opts.ApprovalRoute != "" && !opts.ApprovalRoute.Valid() {
		return fmt.Errorf("invalid approval route %q", opts.ApprovalRoute)
	}
	launch := opts.UnattendedLaunch
	if launch.IsZero() {
		if opts.ApprovalRoute != "" {
			if want := launchcontract.ResolveApprovalRoute(opts.YoloMode, opts.AutoApprove, launch); opts.ApprovalRoute != want {
				return fmt.Errorf("approval route %q does not match effective launch route %q", opts.ApprovalRoute, want)
			}
		}
		return nil
	}
	if err := launch.Validate(); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(opts.Agent), strings.TrimSpace(launch.Agent)) {
		return fmt.Errorf("unattended launch agent %q does not match spawn agent %q", launch.Agent, opts.Agent)
	}
	if opts.AutoApprove || opts.TrustWorkingDirectory || strings.TrimSpace(opts.Model) != "" ||
		strings.TrimSpace(opts.Effort) != "" || strings.TrimSpace(opts.Executable) != "" {
		return errors.New("unattended launch policy must not be duplicated in spawn options")
	}
	if opts.ApprovalRoute != "" {
		if want := launchcontract.ResolveApprovalRoute(opts.YoloMode, opts.AutoApprove, launch); opts.ApprovalRoute != want {
			return fmt.Errorf("approval route %q does not match effective launch route %q", opts.ApprovalRoute, want)
		}
	}
	return nil
}

type AttachInfo struct {
	LastSeq         uint32
	Cols            uint16
	Rows            uint16
	PID             int
	Running         bool
	ExitCode        *int
	ExitSignal      *string
	GhosttySnapshot []byte
	// Empty when absent, and from an old worker that does not send it.
	GhosttySnapshotFormat string
	// Rows are SCREEN-space, captured atomically with the snapshot and LastSeq.
	GhosttyBlocks              []pty.AttachBlockData
	GhosttyPlacements          []pty.KittyPlacement
	GhosttyScrollbackTruncated bool
}

// Zero preserves mixed-version full attaches.
type AttachOptions struct {
	OmitReplay bool
}

type OutputEvent struct {
	Kind   string
	Data   []byte
	Seq    uint32
	Reason string
	// An empty set is how a client learns the last image is gone.
	Placements []pty.KittyPlacement
}

type SessionInfo struct {
	SessionID string
	Agent     string
	CWD       string

	Running bool
	State   string

	// Evidence, not a state claim.
	LastSignal    pty.Observation
	HasLastSignal bool

	Cols    uint16
	Rows    uint16
	PID     int
	LastSeq uint32

	ExitCode   *int
	ExitSignal *string
}

type Stream interface {
	Events() <-chan OutputEvent
	Close() error
}

type RecoveryReport struct {
	Recovered int
	Pruned    int
	Missing   int
	Failed    int
}

type ExitInfo struct {
	ID          string
	ExitCode    int
	Signal      string
	LifecycleID string
}

type Backend interface {
	Spawn(ctx context.Context, opts SpawnOptions) error
	Attach(ctx context.Context, sessionID, subscriberID string, opts ...AttachOptions) (AttachInfo, Stream, error)
	Input(ctx context.Context, sessionID string, data []byte) error
	// xpixel/ypixel are total device pixels, 0 when unknown.
	Resize(ctx context.Context, sessionID string, cols, rows, xpixel, ypixel uint16) (bool, error)
	// Best-effort: a worker predating the method returns nil.
	SetTheme(ctx context.Context, sessionID string, theme pty.TerminalTheme) error
	// Returns nil only after the child process has exited.
	Kill(ctx context.Context, sessionID string, sig syscall.Signal) error
	Remove(ctx context.Context, sessionID string) error
	SessionIDs(ctx context.Context) []string
	Recover(ctx context.Context) (RecoveryReport, error)
	Shutdown(ctx context.Context) error
}

type LifecycleHooks interface {
	SetExitHandler(func(ExitInfo))
	SetStateHandler(func(sessionID string, obs pty.Observation))
}

type SessionInfoProvider interface {
	SessionInfo(ctx context.Context, sessionID string) (SessionInfo, error)
}

// Authoritative after a daemon restart; the durable launch intent is the fallback.
type SessionLaunchParams struct {
	// False when the worker predates launch-param recording: the daemon must
	// abort the reload rather than respawn with defaults.
	Recorded          bool
	YoloMode          bool
	ApprovalRoute     launchcontract.ApprovalRoute
	Executable        string
	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	Model             string
	Effort            string
	UnattendedLaunch  launchcontract.UnattendedLaunchSpec
}

type SessionLaunchParamsProvider interface {
	SessionLaunchParams(ctx context.Context, sessionID string) (SessionLaunchParams, error)
}

type WorkerProcessProvider interface {
	WorkerPIDs(ctx context.Context) map[string]int
}

type ScreenSnapshotProvider interface {
	ScreenSnapshot(ctx context.Context, sessionID string) (pty.ScreenSnapshotInfo, error)
}

// Optional; on error the caller drops that placement's render.
type KittyImageProvider interface {
	KittyImage(ctx context.Context, sessionID string, imageID uint32) (pty.KittyImage, error)
}

// A worker outlives an install, so after a ghostty bump it and the daemon stop
// agreeing about the grid. known=false is "not yet known", not a verdict.
type TerminalBuildProvider interface {
	SessionTerminalBuild(sessionID string) (format string, known bool)
}

// Optional. A shared runtime can keep an older terminal model alive and replay
// it portably when the daemon no longer understands its native snapshot.
type TerminalBuildCompatibilityProvider interface {
	SessionCanReplayWithFormat(sessionID, format string) bool
}

// Replaces the worker process image in place: same pid, same PTY, same child.
type WorkerUpgrader interface {
	UpgradeWorker(ctx context.Context, sessionID string) error
}

type SessionLivenessProber interface {
	SessionLikelyAlive(ctx context.Context, sessionID string) (bool, error)
}

type RecoverableRuntime interface {
	Backend
	SessionInfoProvider
	SessionLivenessProber
}

type ModeProvider interface {
	PTYBackendMode() string
}
