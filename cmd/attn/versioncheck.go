package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
)

func warnIfDaemonVersionMismatch() {
	if !comparableBuildVersion(strings.TrimSpace(buildinfo.Version)) {
		return
	}
	if msg, ok := versionMismatchWarning(buildinfo.Version, fetchDaemonVersion()); ok {
		fmt.Fprintln(os.Stderr, msg)
	}
}

func versionMismatchWarning(cliVersion, daemonVersion string) (string, bool) {
	cliVersion = strings.TrimSpace(cliVersion)
	daemonVersion = strings.TrimSpace(daemonVersion)
	if !comparableBuildVersion(cliVersion) || !comparableBuildVersion(daemonVersion) {
		return "", false
	}
	if cliVersion == daemonVersion {
		return "", false
	}
	return fmt.Sprintf(
		"attn: warning: this CLI is %s but the running attn app is %s. "+
			"You may be running a stale attn — check `which -a attn`, then reinstall or use $ATTN_WRAPPER_PATH.",
		cliVersion, daemonVersion), true
}

func comparableBuildVersion(v string) bool {
	switch v {
	case "", "dev", "unknown":
		return false
	default:
		return true
	}
}

// Targets 127.0.0.1 directly so the probe works with the daemon bound to 0.0.0.0.
func fetchDaemonVersion() string {
	httpClient := &http.Client{Timeout: 400 * time.Millisecond}
	url := "http://" + net.JoinHostPort("127.0.0.1", config.WSPort()) + "/health"
	resp, err := httpClient.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var health struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return ""
	}
	return strings.TrimSpace(health.Version)
}
