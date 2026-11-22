package harness

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/launchenv"
)

// LauncherLaunch is a launch through attn's launcher, which starts the harness inside its terminal.
type LauncherLaunch struct {
	Label         string
	Conversation  string
	Picker        bool
	Yolo          bool
	InitialPrompt string
	ExecutableEnv string
	Executable    string
	AutoApprove   bool
	Model         string
	Effort        string
	Target        string
	ContextCap    int
	Env           []string
}

// LauncherCommand runs attn's launcher in a login shell, which builds the harness's command in the terminal.
func LauncherCommand(l LauncherLaunch) Command {
	argv := []string{launchenv.ActiveAttnExecutable(), "-s", l.Label}
	if l.Conversation != "" {
		argv = append(argv, "--resume", l.Conversation)
	} else if l.Picker {
		argv = append(argv, "--resume")
	}
	if l.Yolo {
		argv = append(argv, "--yolo")
	}
	if l.InitialPrompt != "" {
		argv = append(argv, "--initial-prompt", l.InitialPrompt)
	}
	env := []string{"ATTN_DAEMON_MANAGED=1", "ATTN_MODEL=" + l.Model, "ATTN_EFFORT=" + l.Effort, "ATTN_EXECUTION_TARGET=" + l.Target, fmt.Sprintf("ATTN_AUTO_COMPACT_WINDOW=%d", l.ContextCap)}
	if l.AutoApprove {
		env = append(env, "ATTN_AUTO_APPROVE=1")
	}
	if l.Executable != "" {
		env = append(env, l.ExecutableEnv+"="+l.Executable)
	}
	return Command{Argv: argv, Env: append(env, l.Env...), LoginShell: true}
}

// DirectCommand runs the harness's own command in a login shell. The executable is the explicit one, else the login environment's executableEnv, else the command's first word.
func DirectCommand(executableEnv, explicitExecutable string, command, env []string) Command {
	argv := append([]string{"/bin/sh", "-c", directLaunchScript, "attn-agent", executableEnv, explicitExecutable}, command...)
	var kept []string
	for _, entry := range env {
		if key, _, _ := strings.Cut(entry, "="); key != executableEnv {
			kept = append(kept, entry)
		}
	}
	return Command{Argv: argv, Env: kept, LoginShell: true}
}

const directLaunchScript = `
trim_space() {
 trimmed=${1#"${1%%[![:space:]]*}"}
 trimmed=${trimmed%"${trimmed##*[![:space:]]}"}
}
executable_var=$1
explicit_executable=$2
shift 2
eval "inherited_executable=\${$executable_var-}"
trim_space "$inherited_executable"
executable=${explicit_executable:-${trimmed:-$1}}
shift
export "$executable_var=$executable"
exec "$executable" "$@"
`
