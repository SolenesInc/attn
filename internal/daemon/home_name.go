package daemon

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func homeDisplayName() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("scutil", "--get", "ComputerName").Output(); err == nil {
			if name := strings.TrimSpace(string(out)); name != "" {
				return name
			}
		}
	}
	host, _ := os.Hostname()
	short, _, _ := strings.Cut(host, ".")
	return short
}
