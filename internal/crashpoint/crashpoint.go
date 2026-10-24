package crashpoint

import (
	"os"
	"strings"
	"syscall"
)

func At(point string) {
	if strings.TrimSpace(os.Getenv("ATTN_CRASH_AT")) != point {
		return
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
