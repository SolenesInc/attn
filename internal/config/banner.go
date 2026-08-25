package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PrintProfileBanner writes a one-line banner to w when a non-default ATTN_PROFILE
// is active. Not from hook commands: they run on every action and flood output.
func PrintProfileBanner(w io.Writer) {
	profile := Profile()
	if profile == "" {
		return
	}
	fmt.Fprintf(w, "[attn profile=%s socket=%s port=%s]\n",
		profile,
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
