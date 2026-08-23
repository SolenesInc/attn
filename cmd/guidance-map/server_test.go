package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/guidance"
)

func TestHandlerServesPageAndScenarioCatalog(t *testing.T) {
	handler := newHandler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Guidance atlas") {
		t.Fatalf("index = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/catalog?home=0&agent=plugin&role=plain&garden=no_plot&plugin_launch=0", nil))
	var snapshot guidance.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Scenario.Home || snapshot.Scenario.Agent != guidance.AgentPlugin || snapshot.Scenario.PluginLaunch {
		t.Fatalf("scenario = %+v", snapshot.Scenario)
	}
}

func TestMockSaveReportsPathWithoutWriting(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/mock-save", bytes.NewBufferString(`{"unit_id":"launch.garden","copy":"changed"}`))
	request.Header.Set("Content-Type", "application/json")
	newHandler().ServeHTTP(response, request)
	var result mockSaveResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Mock || !result.Changed || result.WouldWrite != "garden-standing.md" {
		t.Fatalf("mock save = %+v", result)
	}
}
