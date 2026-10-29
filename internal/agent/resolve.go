package agent

import (
	"os"
	"strings"
)

func resolveExec(envVar, configured, fallback string) string {
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	if envVar != "" {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			return v
		}
	}
	return fallback
}
