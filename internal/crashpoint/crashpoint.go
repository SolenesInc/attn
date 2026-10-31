package crashpoint

import (
	"os"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/pausepoint"
)

func At(point string) {
	if strings.TrimSpace(os.Getenv("ATTN_CRASH_AT")) != point {
		return
	}
	pausepoint.At(point)
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
