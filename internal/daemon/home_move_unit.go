package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

// homeUnitName is the systemd user unit that starts this instance's daemon once it holds the home.
func homeUnitName() string {
	if instance := config.Instance(); instance != "" && instance != "default" {
		return "attn-" + instance + ".service"
	}
	return "attn.service"
}

// installHomeUnit has systemd start this daemon at boot on a Linux machine that becomes the home (D8). Nothing dials
// a home, so nothing else would start it. The unit carries the login PATH and ssh agent socket this daemon has, so
// the home runs gh and reaches its outposts with the user's keys (F13). It restarts the daemon whenever it ends,
// with no start limit, so the start that waits for this daemon to exit after the commit succeeds once it has.
func (d *Daemon) installHomeUnit(ctx context.Context, start bool) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.UserConfigDir()
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
	path := filepath.Join(dir, "systemd", "user", homeUnitName())
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(homeUnitText(executable, env)), 0o600); err != nil {
		return err
	}
	calls := [][]string{{"--user", "daemon-reload"}, {"--user", "enable", homeUnitName()}}
	if start {
		calls = append(calls, []string{"--user", "start", "--no-block", homeUnitName()})
	}
	for _, args := range calls {
		if err := d.runOnThisMachine(ctx, "systemctl", args...); err != nil {
			return err
		}
	}
	return nil
}

// keepHomeUnit installs the unit of a home that lacks it, as one that stopped between its commit and the install
// does, without starting it: this daemon already runs the home.
func (d *Daemon) keepHomeUnit(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	if fileExists(filepath.Join(dir, "systemd", "user", homeUnitName())) {
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
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "systemd", "user", homeUnitName())
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := d.runOnThisMachine(ctx, "systemctl", "--user", "disable", homeUnitName()); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return d.runOnThisMachine(ctx, "systemctl", "--user", "daemon-reload")
}

func homeUnitText(executable string, env map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=attn home\nStartLimitIntervalSec=0\n\n[Service]\nExecStart=%s daemon\n", systemdQuote(executable))
	for _, key := range []string{"ATTN_INSTANCE", "ATTN_DATA_DIR", "PATH", "SSH_AUTH_SOCK"} {
		if value, ok := env[key]; ok {
			fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(key+"="+value))
		}
	}
	b.WriteString("Restart=always\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func systemdQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(value) + `"`
}
