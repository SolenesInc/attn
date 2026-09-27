package testworld

import (
	"os/exec"
	"syscall"
)

func dieWithTestProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
