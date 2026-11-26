package harness

import "strings"

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
