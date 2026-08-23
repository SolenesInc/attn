package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/victorarias/attn/internal/guidance"
)

//go:embed web/index.html
var webFiles embed.FS

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /api/catalog", handleCatalog)
	mux.HandleFunc("POST /api/mock-save", handleMockSave)
	return mux
}

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	data, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func handleCatalog(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	defaults := guidance.DefaultScenario()
	scenario := guidance.Scenario{
		Home:              boolean(query.Get("home"), defaults.Home),
		Agent:             guidance.Agent(query.Get("agent")),
		Role:              guidance.Role(query.Get("role")),
		Garden:            guidance.GardenState(query.Get("garden")),
		HasContext:        boolean(query.Get("context"), defaults.HasContext),
		WorkflowEnabled:   boolean(query.Get("workflow"), defaults.WorkflowEnabled),
		LaunchHasGuidance: boolean(query.Get("launch_guidance"), defaults.LaunchHasGuidance),
		PluginLaunch:      boolean(query.Get("plugin_launch"), defaults.PluginLaunch),
		PluginInitial:     boolean(query.Get("plugin_initial"), defaults.PluginInitial),
		PluginMessages:    boolean(query.Get("plugin_messages"), defaults.PluginMessages),
	}
	snapshot, err := guidance.Catalog(scenario)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, snapshot)
}

func boolean(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

type mockSaveRequest struct {
	UnitID string `json:"unit_id"`
	Copy   string `json:"copy"`
}

type mockSaveResponse struct {
	Mock       bool   `json:"mock"`
	UnitID     string `json:"unit_id"`
	WouldWrite string `json:"would_write"`
	Bytes      int    `json:"bytes"`
	Changed    bool   `json:"changed"`
}

func handleMockSave(w http.ResponseWriter, r *http.Request) {
	var request mockSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "decode mock save: "+err.Error(), http.StatusBadRequest)
		return
	}
	snapshot, err := guidance.Catalog(guidance.DefaultScenario())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, unit := range snapshot.Units {
		if unit.ID != strings.TrimSpace(request.UnitID) {
			continue
		}
		writeJSON(w, mockSaveResponse{
			Mock: true, UnitID: unit.ID, WouldWrite: unit.CopyPath,
			Bytes: len(request.Copy), Changed: request.Copy != unit.Copy,
		})
		return
	}
	http.Error(w, fmt.Sprintf("unknown guidance unit %q", request.UnitID), http.StatusNotFound)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}
