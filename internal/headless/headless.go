// Package headless owns the switch over headless tasks the daemon starts on its own, with no session or PTY.
package headless

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

const EnvVar = "ATTN_HEADLESS_TASKS"

const SettingKey = "headless_tasks.enabled"

var ErrRefused = errors.New("headless tasks are off")

// The zero value is "on": a process that never mirrors a setting runs headless
// tasks, which is what every non-daemon caller expects.
var storedOff atomic.Bool

func SetStoredEnabled(enabled bool) {
	storedOff.Store(!enabled)
}

func Enabled() bool {
	enabled, _ := resolve()
	return enabled
}

func Mode() string {
	if Enabled() {
		return "on"
	}
	return "off"
}

func Describe() string {
	enabled, source := resolve()
	if enabled {
		return "on"
	}
	return "off (" + source + ")"
}

func resolve() (enabled bool, source string) {
	if value, ok := ParseSwitch(os.Getenv(EnvVar)); ok {
		return value, EnvVar
	}
	if storedOff.Load() {
		return false, SettingKey
	}
	return true, "default"
}

func ParseSwitch(raw string) (value bool, ok bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "on", "1", "true", "yes", "enabled":
		return true, true
	case "off", "0", "false", "no", "disabled":
		return false, true
	}
	return false, false
}

func Refusal(caller string) error {
	return fmt.Errorf("headless task refused (%s): %w", caller, ErrRefused)
}
