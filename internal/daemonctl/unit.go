package daemonctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
)

const homeUnitEnv = "ATTN_HOME_UNIT"

// HomeUnitName is the systemd user unit that starts this instance's daemon once this machine holds the home.
func HomeUnitName() string {
	if instance := config.Instance(); instance != "" && instance != "default" {
		return "attn-" + instance + ".service"
	}
	return "attn.service"
}

func HomeUnitPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "systemd", "user", HomeUnitName()), nil
}

// HomeUnitText runs this binary's daemon for the data folder in env, restarting it whenever it ends. The daemon it
// starts waits for any other daemon of the folder to exit instead of failing, so the unit never restarts in a loop.
func HomeUnitText(executable string, env map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=attn home\nStartLimitIntervalSec=0\n\n[Service]\nExecStart=%s daemon\n", systemdQuote(executable))
	fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(homeUnitEnv+"=1"))
	for _, key := range []string{"ATTN_INSTANCE", "ATTN_DATA_DIR", "PATH", "SSH_AUTH_SOCK"} {
		if value, ok := env[key]; ok {
			fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(key+"="+value))
		}
	}
	b.WriteString("Restart=always\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// TakeHomeUnitStart reports whether this daemon's home unit started it, and keeps the processes it starts from
// inheriting that.
func TakeHomeUnitStart() bool {
	started := os.Getenv(homeUnitEnv) == "1"
	os.Unsetenv(homeUnitEnv)
	return started
}

func systemdQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(value) + `"`
}

// installedHomeUnit is this daemon's home unit when this machine holds the home and the unit runs this data folder.
func installedHomeUnit() (string, bool) {
	if runtime.GOOS != "linux" {
		return "", false
	}
	path, err := HomeUnitPath()
	if err != nil {
		return "", false
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	dataDir := "Environment=" + systemdQuote("ATTN_DATA_DIR="+config.DataDir())
	for _, line := range strings.Split(string(text), "\n") {
		if line == dataDir {
			return HomeUnitName(), true
		}
	}
	return "", false
}

// startHomeUnit has systemd start the daemon, so the unit runs the only daemon of this data folder, and waits until
// it answers.
func startHomeUnit(ctx context.Context, unit string) error {
	if output, err := exec.CommandContext(ctx, "systemctl", "--user", "start", unit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user start %s: %w: %s", unit, err, strings.TrimSpace(string(output)))
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := waitForMatchingDaemon(ctx, ticker.C, fetchHealth, func() bool {
			return isSocketLive(config.SocketPath())
		}, pidLockAvailable)
		if !errors.Is(err, errDaemonLockReleased) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s to start the daemon: %w", unit, ctx.Err())
		case <-ticker.C:
		}
	}
}
