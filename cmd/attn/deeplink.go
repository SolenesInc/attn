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
	return launchInstanceApp(config.Instance(), deepLink)
}

func launchInstanceApp(instance string, args ...string) error {
	executable := config.AppExecutableForInstance(instance)
	if _, err := os.Stat(executable); err != nil {
		return fmt.Errorf("no app installed for instance %s at %s (run make install%s)",
			config.InstanceLabel(), executable, instanceSuffix(instance))
	}

	dataDir := config.DataDirForInstance(instance)
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
