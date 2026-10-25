package daemonctl

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

const harnessLifelineFDEnv = "ATTN_HARNESS_LIFELINE_FD"

func ExitWhenHarnessGoes() error {
	raw := strings.TrimSpace(os.Getenv(harnessLifelineFDEnv))
	if raw == "" {
		return nil
	}
	os.Unsetenv(harnessLifelineFDEnv)
	fd, err := strconvAtoi(raw)
	if err != nil || fd < 3 {
		return fmt.Errorf("invalid %s %q", harnessLifelineFDEnv, raw)
	}
	syscall.CloseOnExec(fd)
	lifeline := os.NewFile(uintptr(fd), "harness-lifeline")
	go func() {
		_, _ = io.Copy(io.Discard, lifeline)
		fmt.Fprintln(os.Stderr, "attn daemon: the test process that started it is gone; exiting")
		os.Exit(1)
	}()
	return nil
}
