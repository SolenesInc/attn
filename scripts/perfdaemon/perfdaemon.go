package perfdaemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/victorarias/attn/internal/config"
)

type Vars struct {
	CPUTimeMS        int64  `json:"cpu_time_ms"`
	DaemonInstanceID string `json:"daemon_instance_id"`
}

func WebSocketURL(writes string) (string, error) {
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
	return "ws://localhost:" + config.WSPortForInstance(instance) + "/ws", nil
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

func RequireSameDaemon(varsURL, wsURL string, initialState []byte) error {
	var initial struct {
		DaemonInstanceID string `json:"daemon_instance_id"`
	}
	if err := json.Unmarshal(initialState, &initial); err != nil {
		return fmt.Errorf("decoding initial_state: %w", err)
	}
	vars, err := ReadVars(varsURL)
	if err != nil {
		return err
	}
	if vars.DaemonInstanceID == "" || vars.DaemonInstanceID != initial.DaemonInstanceID {
		return fmt.Errorf("-vars %s serves daemon %q, but the WebSocket %s serves daemon %q; point -vars at the daemon of instance %s", varsURL, vars.DaemonInstanceID, wsURL, initial.DaemonInstanceID, config.Instance())
	}
	return nil
}
