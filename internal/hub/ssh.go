package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func sshBaseArgs(target string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		target,
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func remoteBinaryName(profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
}

func remoteShellEnvScript(profile string) string {
	assignments := make([]string, 0, 5)
	if p := strings.TrimSpace(profile); p != "" {
		assignments = append(assignments, "export ATTN_PROFILE="+shellQuote(p))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_ATTN_BIN")); value != "" {
		assignments = append(assignments, "export ATTN_REMOTE_ATTN_BIN="+shellQuote(value))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_SOCKET_PATH")); value != "" {
		assignments = append(assignments, "export ATTN_SOCKET_PATH="+shellQuote(value))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_WS_PORT")); value != "" {
		assignments = append(assignments, "export ATTN_WS_PORT="+shellQuote(value))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_DB_PATH")); value != "" {
		assignments = append(assignments, "export ATTN_DB_PATH="+shellQuote(value))
	}
	// Deliberately not an ATTN_REMOTE_ name: one export governs both ends, "0"
	// included, which disables the protocol on the remote as it does locally.
	if value := strings.TrimSpace(os.Getenv("ATTN_KITTY_STORAGE_LIMIT")); value != "" {
		assignments = append(assignments, "export ATTN_KITTY_STORAGE_LIMIT="+shellQuote(value))
	}
	if len(assignments) == 0 {
		return ""
	}
	return strings.Join(assignments, "; ") + "; "
}

func remoteShellCommand(profile, script string) string {
	if envScript := remoteShellEnvScript(profile); envScript != "" {
		script = envScript + script
	}
	return "sh -lc " + shellQuote(script)
}

func remoteAttnCommand(profile string, args ...string) string {
	binName := remoteBinaryName(profile)
	bin := fmt.Sprintf(`ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"; if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then ATTN_BIN="$(command -v %s 2>/dev/null || true)"; fi; if [ -z "$ATTN_BIN" ] || [ ! -x "$ATTN_BIN" ]; then printf 'missing attn binary\n' >&2; exit 127; fi; "$ATTN_BIN"`,
		binName,
		binName,
	)
	for _, arg := range args {
		bin += " " + shellQuote(arg)
	}
	return bin
}

// runSSHExit keeps the exit code runSSH collapses: a remote `attn` answers refusals in
// exit codes, which must be told apart from "this binary has no such command".
func runSSHExit(ctx context.Context, target, profile, script string) (stdout, stderr string, exitCode int, err error) {
	var errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "ssh", append(sshBaseArgs(target), remoteShellCommand(profile, script))...)
	cmd.Stderr = &errBuf
	out, runErr := cmd.Output()
	stdout = strings.TrimSpace(string(out))
	stderr = strings.TrimSpace(errBuf.String())
	if runErr == nil {
		return stdout, stderr, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return stdout, stderr, exitErr.ExitCode(), nil
	}
	return stdout, stderr, -1, runErr
}

func (m *Manager) remoteClientToken(ctx context.Context, target, profile string) string {
	stdout, stderr, code, err := runSSHExit(ctx, target, profile, remoteAttnCommand(profile, "client-token"))
	if err != nil {
		m.logf("hub: remote client-token on %s failed: %v", target, err)
		return ""
	}
	if code != 0 {
		m.logf("hub: remote client-token on %s exited %d: %s", target, code, stderr)
		return ""
	}
	return strings.TrimSpace(stdout)
}

func runSSH(ctx context.Context, target, profile, script string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ssh", append(sshBaseArgs(target), remoteShellCommand(profile, script))...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(string(out)), nil
}
