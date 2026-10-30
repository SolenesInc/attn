package outpostssh

import (
	"context"
	"os/exec"
)

func execBusctl(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, "busctl", "get-property", "org.freedesktop.login1", "/org/freedesktop/login1", "org.freedesktop.login1.Manager", "KillUserProcesses")
}
