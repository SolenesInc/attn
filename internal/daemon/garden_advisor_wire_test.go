package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheGardenAdvisorSettingRefusesWhatCannotRunAndKeepsTheSavedRecipe(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	if got := gardenAdvisorRecipe(t, app.Initial.Settings); got != (gardenAdvisorConfig{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"}) {
		t.Errorf("with nothing saved the app is sent recipe %+v, want the Codex default", got)
	}

	t.Setenv("ATTN_CODEX_EXECUTABLE", "")
	codex := filepath.Join(t.TempDir(), "custom-codex")
	gardenAdvisorWriteExecutable(t, codex)
	setSetting(t, app, "codex_executable", codex)
	if err := os.Remove(codex); err != nil {
		t.Fatal(err)
	}
	if refused := gardenAdvisorSetSetting(app, "garden.advisor", `{"agent":"codex"}`); protocol.Deref(refused.Success) {
		t.Fatal("a Codex advisor was saved while the configured Codex executable is missing")
	}
	gardenAdvisorWriteExecutable(t, codex)
	saved := `{"agent":"codex","model":"gpt-custom","effort":"high"}`
	setSetting(t, app, "garden.advisor", saved)

	refused := gardenAdvisorSetSetting(app, "garden.advisor", `{"agent":"shell","model":"anything"}`)
	if protocol.Deref(refused.Success) || !strings.Contains(protocol.Deref(refused.Error), "not supported") {
		t.Fatalf("an unsupported advisor = %+v, want it refused as not supported", refused)
	}
	if got := gardenAdvisorRecipe(t, w.App().Initial.Settings); got != (gardenAdvisorConfig{Agent: "codex", Model: "gpt-custom", Effort: "high"}) {
		t.Errorf("after the refusal the saved recipe is %+v, want the one saved before", got)
	}
}

type gardenAdvisorConfig struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func gardenAdvisorRecipe(t *testing.T, settings protocol.RecordString) gardenAdvisorConfig {
	t.Helper()
	raw, ok := settings["garden.advisor"].(string)
	if !ok {
		t.Fatalf("the settings carry no Garden advisor recipe: %v", settings["garden.advisor"])
	}
	var recipe gardenAdvisorConfig
	if err := json.Unmarshal([]byte(raw), &recipe); err != nil {
		t.Fatalf("the advisor recipe %q does not decode: %v", raw, err)
	}
	return recipe
}

func gardenAdvisorSetSetting(app *testworld.Peer, key, value string) protocol.SettingsUpdatedMessage {
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
}

func gardenAdvisorWriteExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
