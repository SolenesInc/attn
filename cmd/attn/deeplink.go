package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/victorarias/attn/internal/config"
)

func launchDeepLink(deepLink string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", deepLink).Run()
	}
	return launchProfileApp(config.Profile(), deepLink)
}

// The second process hands its argv to the running instance and exits; with no
// instance running it becomes the app, so it must outlive this command.
func launchProfileApp(profile string, args ...string) error {
	executable := config.AppExecutableForProfile(profile)
	if _, err := os.Stat(executable); err != nil {
		return fmt.Errorf("no app installed for profile %s at %s (run make install%s)",
			config.ProfileLabel(), executable, profileSuffix(profile))
	}

	dataDir := config.DataDirForProfile(profile)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dataDir, err)
	}
	logPath := filepath.Join(dataDir, "app.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", logPath, err)
	}
	defer logFile.Close()

	cmd := exec.Command(executable, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
