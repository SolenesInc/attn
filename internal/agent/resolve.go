package agent

import (
	"os"
	"strings"
)

func resolveExec(envVar, configured, fallback string) string {
	if envVar != "" {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	return fallback
}
