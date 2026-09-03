package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/victorarias/attn/internal/launchenv"
)

// PreparedLaunchAttempt is a fully resolved child command. A PTY runtime must
// try attempts in order and report which one started.
type PreparedLaunchAttempt struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Env        []string `json:"env"`
	CWD        string   `json:"cwd"`
	CleanupDir string   `json:"cleanup_dir,omitempty"`
	ShellPath  string   `json:"shell_path"`
}

type PreparedLaunch struct {
	Agent    string                  `json:"agent"`
	Attempts []PreparedLaunchAttempt `json:"attempts"`
}

func PrepareLaunch(opts SpawnOptions, logf LogFunc) (PreparedLaunch, error) {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	agent := normalizeAgent(opts.Agent, len(opts.ExternalCommand) > 0)
	attnPath := launchenv.ActiveAttnExecutable()
	loginShell := GetUserLoginShell()
	cmdEnv := buildSpawnEnv(loginShell, opts, agent, attnPath, logf)

	prepared := PreparedLaunch{Agent: agent}
	shellCandidates := preferredShellCandidates(loginShell)
	if len(opts.ExternalCommand) > 0 && len(shellCandidates) > 1 {
		shellCandidates = shellCandidates[:1]
	}
	var preparationFailures []string
	for _, shellPath := range shellCandidates {
		attemptEnv := cmdEnv
		var (
			cmd        *exec.Cmd
			cleanupDir string
		)
		if agent == "shell" {
			launch, err := prepareShellPaneLaunch(shellPath, cmdEnv)
			if err != nil {
				preparationFailures = append(preparationFailures, fmt.Sprintf("%s: %v", shellPath, err))
				logf("pty launch: could not prepare shell=%s: %v; trying fallback shell", shellPath, err)
				continue
			}
			cmd = launch.command
			attemptEnv = launch.env
			cleanupDir = launch.overlayDir
		} else {
			cmd = buildSpawnCommand(opts, agent, shellPath, attnPath, cmdEnv)
		}

		dir := opts.CWD
		if strings.TrimSpace(opts.ExternalCWD) != "" {
			dir = opts.ExternalCWD
		}
		prepared.Attempts = append(prepared.Attempts, PreparedLaunchAttempt{
			Executable: cmd.Path,
			Args:       append([]string(nil), cmd.Args...),
			Env:        append([]string(nil), attemptEnv...),
			CWD:        dir,
			CleanupDir: cleanupDir,
			ShellPath:  shellPath,
		})
	}
	if len(prepared.Attempts) == 0 {
		if len(preparationFailures) > 0 {
			return PreparedLaunch{}, fmt.Errorf("no terminal shell could be prepared: %s", strings.Join(preparationFailures, "; "))
		}
		return PreparedLaunch{}, errors.New("no shell candidates available")
	}
	return prepared, nil
}

// CleanupExcept removes shell startup overlays for every attempt except keep.
// Pass -1 when no child started.
func (p PreparedLaunch) CleanupExcept(keep int) {
	for i, attempt := range p.Attempts {
		if i == keep || attempt.CleanupDir == "" {
			continue
		}
		_ = os.RemoveAll(attempt.CleanupDir)
	}
}

func commandFromPrepared(attempt PreparedLaunchAttempt) *exec.Cmd {
	args := attempt.Args
	if len(args) > 0 {
		args = args[1:]
	}
	cmd := exec.Command(attempt.Executable, args...)
	cmd.Dir = attempt.CWD
	cmd.Env = attempt.Env
	return cmd
}
