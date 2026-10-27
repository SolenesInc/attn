package testworld

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/victorarias/attn/internal/bridge"
)

func (s *Stack) SuperviseUpdates() {
	s.T.Helper()
	data, err := os.ReadFile(s.binary)
	if err != nil {
		s.T.Fatal(err)
	}
	s.binary = filepath.Join(s.Dir, "bin", "attn-daemon")
	if err := os.WriteFile(s.binary, data, 0755); err != nil {
		s.T.Fatal(err)
	}
	s.supervised = true
	s.Vars = append(s.Vars, "INVOCATION_ID=testworld-systemd")
}

func runSupervisor() {
	binary := os.Getenv("ATTN_TESTWORLD_SUPERVISE")
	if binary == "" {
		return
	}
	_ = os.Unsetenv("ATTN_TESTWORLD_SUPERVISE")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	files := []*os.File{os.NewFile(3, "ready"), os.NewFile(4, "websocket"), os.NewFile(5, "lifeline")}
	for {
		cmd := exec.Command(binary, "daemon")
		cmd.Env = os.Environ()
		cmd.ExtraFiles = files
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case <-stop:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			<-exited
			os.Exit(0)
		case err := <-exited:
			var status *exec.ExitError
			if !errors.As(err, &status) || status.ExitCode() != bridge.UpdateRestartStatus {
				if err == nil {
					os.Exit(0)
				}
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Fprintln(os.Stderr, "systemd supervisor: restart after exit "+strconv.Itoa(bridge.UpdateRestartStatus))
			_ = os.Unsetenv("ATTN_DAEMON_READY_FD")
		}
	}
}
