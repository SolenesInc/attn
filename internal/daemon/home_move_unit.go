package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemonctl"
)

// installHomeUnit has systemd start this daemon at boot on a Linux machine that becomes the home (D8). Nothing dials
// a home, so nothing else would start it. The unit carries the login PATH and ssh agent socket this daemon has, so
// the home runs gh and reaches its outposts with the user's keys (F13). It restarts the daemon whenever it ends,
// and its daemon waits for this one to exit after the commit.
func (d *Daemon) installHomeUnit(ctx context.Context, start bool) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	path, err := daemonctl.HomeUnitPath()
	if err != nil {
		return err
	}
	env := map[string]string{"ATTN_DATA_DIR": d.dataRoot}
	if instance := config.Instance(); instance != "" {
		env["ATTN_INSTANCE"] = instance
	}
	for _, pair := range d.loginEnv.Latest(ctx) {
		key, value, _ := strings.Cut(pair, "=")
		if key == "PATH" || key == "SSH_AUTH_SOCK" {
			env[key] = value
		}
	}
	if _, ok := env["SSH_AUTH_SOCK"]; !ok {
		if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
			env["SSH_AUTH_SOCK"] = socket
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(daemonctl.HomeUnitText(executable, env)), 0o600); err != nil {
		return err
	}
	calls := [][]string{{"--user", "daemon-reload"}, {"--user", "enable", daemonctl.HomeUnitName()}}
	if start {
		calls = append(calls, []string{"--user", "start", "--no-block", daemonctl.HomeUnitName()})
	}
	for _, args := range calls {
		if err := d.runOnThisMachine(ctx, "systemctl", args...); err != nil {
			return err
		}
	}
	return nil
}

// keepHomeUnit installs the unit of a home that systemd does not start at boot, as one that stopped between its
// commit and the end of the install does, without starting it: this daemon already runs the home. It asks systemd
// with this daemon's own environment, so a start does not wait for the login shell.
func (d *Daemon) keepHomeUnit(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if exec.CommandContext(ctx, "systemctl", "--user", "is-enabled", "--quiet", daemonctl.HomeUnitName()).Run() == nil {
		return nil
	}
	return d.installHomeUnit(ctx, false)
}

// removeHomeUnit stops systemd from starting this daemon at boot once this machine no longer holds the home. The
// running daemon keeps running; the home starts it over ssh when it needs it.
func (d *Daemon) removeHomeUnit(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	path, err := daemonctl.HomeUnitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := d.runOnThisMachine(ctx, "systemctl", "--user", "disable", daemonctl.HomeUnitName()); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return d.runOnThisMachine(ctx, "systemctl", "--user", "daemon-reload")
}
