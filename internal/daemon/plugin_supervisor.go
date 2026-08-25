package daemon

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/supervise"
)

const (
	pluginDesiredRunning = supervise.DesiredRunning
	pluginDesiredStopped = supervise.DesiredStopped
)

const (
	pluginPhaseStarting  = supervise.PhaseStarting
	pluginPhaseConnected = supervise.PhaseConnected
	pluginPhaseBackoff   = supervise.PhaseBackoff
	pluginPhaseStopped   = supervise.PhaseStopped
	pluginPhaseParked    = supervise.PhaseParked
)

var pluginRestartBackoff = supervise.RestartBackoff

const pluginDisconnectGrace = supervise.DisconnectGrace

type pluginExit = supervise.Exit
type pluginRuntimeSnapshot = supervise.Snapshot
type pluginProcessHandle = supervise.Process
type pluginSupervisorTimer = supervise.Timer
type pluginSupervisorClock = supervise.Clock

type pluginProcessLauncher interface {
	Start(manifest pluginManifest, env []string, log io.Writer) (pluginProcessHandle, error)
}

type execPluginProcessLauncher struct {
	registryDir string
}

func (l execPluginProcessLauncher) Start(manifest pluginManifest, env []string, log io.Writer) (pluginProcessHandle, error) {
	var cmd *exec.Cmd
	switch manifest.Plugin.Kind {
	case plugins.EntrypointExecutable:
		cmd = exec.Command(filepath.Join(manifest.Dir, manifest.Plugin.Path))
	case plugins.EntrypointBun:
		cmd = exec.Command("/usr/bin/env", "bun", "run", manifest.Plugin.Path)
	default:
		return nil, fmt.Errorf("start plugin %q: unsupported entrypoint kind %q", manifest.Name, manifest.Plugin.Kind)
	}
	cmd.Dir = manifest.Dir
	cmd.Env = env
	// Group leadership is what lets the reaper sweep whatever the driver spawned,
	// without touching the daemon's other children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process, err := supervise.StartCommand(cmd, log)
	if err != nil {
		return nil, fmt.Errorf("start %s plugin process: %w", manifest.Plugin.Kind, err)
	}
	if l.registryDir != "" {
		pid := cmd.Process.Pid
		// The path carries the pid so a restart's fresh record and the old process's
		// removal (in Wait, which can straddle the respawn) never touch the same file.
		path := filepath.Join(l.registryDir, fmt.Sprintf("%s-%d.json", manifest.Name, pid))
		if err := procreap.WriteEntry(path, procreap.NewEntry(manifest.Name, pid, pid, cmd.Args)); err == nil {
			return &reapRegisteredProcess{Process: process, pid: pid, registryPath: path}, nil
		}
	}
	return process, nil
}

type reapRegisteredProcess struct {
	supervise.Process
	pid          int
	registryPath string
}

func (p *reapRegisteredProcess) Wait() pluginExit {
	exit := p.Process.Wait()
	_ = procreap.RemoveEntry(p.registryPath)
	return exit
}

type pluginSupervisor struct {
	*supervise.Supervisor
	launcher pluginProcessLauncher
	env      func(pluginManifest, uint64) []string
}

func newPluginSupervisor(
	launcher pluginProcessLauncher,
	clock pluginSupervisorClock,
	env func(pluginManifest, uint64) []string,
	options supervise.Options,
) *pluginSupervisor {
	if launcher == nil {
		launcher = execPluginProcessLauncher{}
	}
	if env == nil {
		env = func(pluginManifest, uint64) []string { return nil }
	}
	options.Clock = clock
	return &pluginSupervisor{
		Supervisor: supervise.New(options),
		launcher:   launcher,
		env:        env,
	}
}

func (s *pluginSupervisor) Ensure(manifest pluginManifest) error {
	return s.Supervisor.Ensure(manifest.Name, func(req supervise.StartRequest) (supervise.Process, error) {
		return s.launcher.Start(manifest, s.env(manifest, req.Generation), req.Log)
	})
}
