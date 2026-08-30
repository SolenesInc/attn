package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// `ps -o comm=` prints the path the process was exec'd from; -ww stops ps trimming
// it to the terminal width.
func processExecutable(pid int) (string, error) {
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", fmt.Errorf("ps -p %d: %w", pid, err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("ps -p %d named no executable", pid)
	}
	return path, nil
}

// A bundled macOS app is quit through its bundle id, not a signal, and the request
// returns before the app is gone: the caller still has to watch the pid.
func requestAppQuit(bundleID string) bool {
	_ = exec.Command("osascript", "-e", fmt.Sprintf("tell application id %q to quit", bundleID)).Run()
	return true
}

// A bundle from before the app wrote app.pid leaves LaunchServices as the only
// witness, so ask it whether the bundle id still runs.
func stopAppWithoutPIDFile(r profileResolved, pidPath string) (string, error) {
	if !fileExists(r.AppPath) {
		return "not running (not installed, no " + pidPath + ")", nil
	}
	running, err := appIsRunning(r.BundleID)
	if err != nil {
		return "", fmt.Errorf("%s wrote no %s and LaunchServices would not say whether it runs (%v); quit it yourself and re-run", r.AppPath, pidPath, err)
	}
	if !running {
		return "not running (no " + pidPath + ")", nil
	}
	requestAppQuit(r.BundleID)
	gone := waitUntil(appStopQuitWait, func() bool {
		running, err := appIsRunning(r.BundleID)
		return err == nil && !running
	})
	if !gone {
		return "", fmt.Errorf("asked %s to quit and it was still running %s later (it wrote no %s to wait on); quit it yourself and re-run", r.BundleID, appStopQuitWait, pidPath)
	}
	return "quit " + r.BundleID, nil
}

func appIsRunning(bundleID string) (bool, error) {
	out, err := exec.Command("osascript", "-e", fmt.Sprintf("application id %q is running", bundleID)).Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}
