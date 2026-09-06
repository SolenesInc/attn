package daemon

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The Sessions list stores its filters as one JSON object so the panel comes
// back the way it was left, on every client that talks to this daemon.
const SettingSessionsFilters = "sessions.filters"

const sessionsFilterDayLayout = "2006-01-02"

var (
	sessionsFilterScopes = []string{"live", "closed", "all"}
	sessionsFilterRanges = []string{"any", "today", "yesterday", "7d", "30d", "custom"}
)

type sessionsFilters struct {
	Scope       string `json:"scope"`
	Range       string `json:"range"`
	CustomFrom  string `json:"customFrom"`
	CustomTo    string `json:"customTo"`
	WorkspaceID string `json:"workspaceId"`
	Repository  string `json:"repository"`
}

func validateSessionsFilters(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	var filters sessionsFilters
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&filters); err != nil {
		return fmt.Errorf("%s must be a session filter object: %v", SettingSessionsFilters, err)
	}
	if decoder.More() {
		return fmt.Errorf("%s must be a single session filter object: %s", SettingSessionsFilters, trimmed)
	}
	if !slices.Contains(sessionsFilterScopes, filters.Scope) {
		return fmt.Errorf("%s: scope must be one of %s, got %q", SettingSessionsFilters, strings.Join(sessionsFilterScopes, ", "), filters.Scope)
	}
	if !slices.Contains(sessionsFilterRanges, filters.Range) {
		return fmt.Errorf("%s: range must be one of %s, got %q", SettingSessionsFilters, strings.Join(sessionsFilterRanges, ", "), filters.Range)
	}
	for _, day := range []struct{ field, value string }{
		{"customFrom", filters.CustomFrom},
		{"customTo", filters.CustomTo},
	} {
		if day.value == "" {
			continue
		}
		if _, err := time.Parse(sessionsFilterDayLayout, day.value); err != nil {
			return fmt.Errorf("%s: %s must be a date like 2026-09-05, got %q", SettingSessionsFilters, day.field, day.value)
		}
	}
	return nil
}
