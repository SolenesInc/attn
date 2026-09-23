package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func PrintInstanceBanner(w io.Writer) {
	instance := Instance()
	if instance == "" {
		return
	}
	fmt.Fprintf(w, "[attn instance=%s socket=%s port=%s]\n",
		instance,
		CollapseHome(SocketPath()),
		WSPort(),
	)
}

func CollapseHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return collapseHomeRelativeTo(path, home)
}

func collapseHomeRelativeTo(path, home string) string {
	home = filepath.Clean(home)
	cleaned := filepath.Clean(path)
	if cleaned == home {
		return "~"
	}
	if strings.HasPrefix(cleaned, home+string(filepath.Separator)) {
		return "~" + cleaned[len(home):]
	}
	return path
}
