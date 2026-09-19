package ptybackend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/pty"
)

const (
	OutputEventKindOutput     = "output"
	OutputEventKindDesync     = "desync"
	OutputEventKindExit       = "exit"
	OutputEventKindResize     = "resize"
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
	DaemonEnv         []string
	LifecycleID       string

	LoginShellEnv []string

	WorkflowGuidanceEnabled bool

	AutoApprove           bool
	ApprovalRoute         launchcontract.ApprovalRoute
	TrustWorkingDirectory bool

	Model string

	Effort string

	ContextWindowCap int

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

func validateSpawnOptions(opts SpawnOptions) error {
	if strings.TrimSpace(opts.CWD) == "" {
		return errors.New("missing cwd")
	}
	if err := validateUnattendedSpawnOptions(opts); err != nil {
		return err
	}
	directory := toPTYSpawnOptions(opts).WorkingDirectory()
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("working directory %q: %w", directory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory %q is not a directory", directory)
	}
	return nil
}

type AttachInfo struct {
	LastSeq                    uint32
	Cols                       uint16
	Rows                       uint16
	PID                        int
	Running                    bool
	ExitCode                   *int
	ExitSignal                 *string
	GhosttySnapshot            []byte
	GhosttySnapshotFormat      string
	GhosttyBlocks              []pty.AttachBlockData
	GhosttyPlacements          []pty.KittyPlacement
	GhosttyScrollbackTruncated bool
}

type AttachOptions struct {
	OmitReplay bool
}

type OutputEvent struct {
	Kind       string
	Data       []byte
	Seq        uint32
	Reason     string
	Cols       uint16
	Rows       uint16
	XPixel     uint16
	YPixel     uint16
	Placements []pty.KittyPlacement
}

type ResizeResult struct {
	Changed       bool
	StreamOrdered bool
}

type SessionInfo struct {
	SessionID string
	Agent     string
	CWD       string

	Running bool
	State   string

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
	Resize(ctx context.Context, sessionID string, cols, rows, xpixel, ypixel uint16) (ResizeResult, error)
	SetTheme(ctx context.Context, sessionID string, theme pty.TerminalTheme) error
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

type SessionLaunchParams struct {
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

type KittyImageProvider interface {
	KittyImage(ctx context.Context, sessionID string, imageID uint32) (pty.KittyImage, error)
}

type TerminalBuildProvider interface {
	SessionTerminalBuild(sessionID string) (format string, known bool)
}

type TerminalBuildCompatibilityProvider interface {
	SessionCanReplayWithFormat(sessionID, format string) bool
}

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
