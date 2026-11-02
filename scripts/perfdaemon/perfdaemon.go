package perfdaemon

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/victorarias/attn/internal/config"
)

type Vars struct {
	CPUTimeMS int64  `json:"cpu_time_ms"`
	WSAddr    string `json:"ws_addr"`
}

func WebSocketURL(writes, varsURL string) (string, error) {
	instance, err := config.NormalizeInstanceName(os.Getenv("ATTN_INSTANCE"))
	if err != nil {
		return "", fmt.Errorf("ATTN_INSTANCE: %w", err)
	}
	if instance == "" {
		return "", fmt.Errorf("set ATTN_INSTANCE to a non-production instance: this tool %s", writes)
	}
	if err := config.ValidateInstanceRouting(); err != nil {
		return "", err
	}
	port := config.WSPortForInstance(instance)
	if varsURL == "" {
		return "ws://localhost:" + port + "/ws", nil
	}
	vars, err := ReadVars(varsURL)
	if err != nil {
		return "", err
	}
	if _, served, _ := net.SplitHostPort(vars.WSAddr); served != port {
		return "", fmt.Errorf("-vars %s belongs to the daemon listening on %q, not instance %s's daemon on port %s; point -vars at that instance's daemon", varsURL, vars.WSAddr, instance, port)
	}
	return "ws://" + vars.WSAddr + "/ws", nil
}

func ReadVars(url string) (Vars, error) {
	resp, err := http.Get(url)
	if err != nil {
		return Vars{}, fmt.Errorf("reading %s: %w", url, err)
	}
	defer resp.Body.Close()
	var out Vars
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Vars{}, fmt.Errorf("decoding %s: %w", url, err)
	}
	return out, nil
}
