package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestUpgradingNamesEachSavedModelByItsCatalogID(t *testing.T) {
	const version = 1795434433082663
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, version-1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before := `{"enabled":true,"roles":[{"id":"build","name":"Build","enabled":true,"default_choice_id":"plain","choices":[
		{"id":"plain","name":"Plain","when":"","selection":{"harness":"sigma","provider":"openai","model":"gpt-5.4","effort":""}}
	]}],"fallback":{"selection":{"harness":"","provider":"","model":"","effort":""},"instructions":""}}`
	latest := `{"enabled":true,"roles":[{"id":"build","name":"Build","enabled":true,"default_choice_id":"plain","choices":[
		{"id":"plain","name":"Plain","when":"","selection":{"harness":"sigma","provider":"openai","model":"gpt-5.5","effort":"high"}},
		{"id":"full","name":"Full","when":"Copy","selection":{"harness":"snipe","provider":"gemini","model":"gemini/gemini-4-carbon","effort":""}},
		{"id":"none","name":"None","when":"Default","selection":{"harness":"claude","provider":"","model":"opus","effort":""}},
		{"id":"codex","name":"Codex","when":"Codex","selection":{"harness":"codex","provider":"","model":"gpt-6.1-sol","effort":"high"}}
	]}],"fallback":{"selection":{"harness":"claude","provider":"","model":"sonnet","effort":""},"instructions":""}}`
	now := time.Now().UTC().Format(sortableTimeFormat)
	for revision, config := range []string{before, latest} {
		if _, err := s.db.Exec(`INSERT INTO delegation_preference_revisions (revision, parent, config, message, created_at) VALUES (?, ?, ?, 'saved', ?)`, revision+1, revision, config, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO delegation_operations (request_id, operation_id, request_json, state, progress, session_id, created_at, updated_at, resolved_preferences) VALUES ('r1', 'op1', '{}', 'accepted', '', 's1', ?, ?, ?)`,
		now, now, `{"selection":{"harness":"sigma","provider":"openai","model":"gpt-5.5","effort":""},"role_name":"Build","instructions":"","revision":2}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('model_tier_overrides', ?)`, `[{"harness":"sigma","provider":"openai","model":"gpt-5.5","tier":"deep"},{"harness":"sigma","provider":"","model":"openai/gpt-5.5","tier":"light"},{"harness":"claude","provider":"","model":"opus","tier":"light"},{"harness":"codex","provider":"","model":"gpt-6.1-sol","tier":"standard"}]`); err != nil {
		t.Fatal(err)
	}

	if err := migrateDBThrough(s.db, path, version); err != nil {
		t.Fatal(err)
	}
	prefs, err := s.GetDelegationPreferences()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{"fallback": prefs.Fallback.Selection.Model}
	for _, choice := range prefs.Roles[0].Choices {
		got[choice.ID] = choice.Selection.Model
	}
	for id, want := range map[string]string{"plain": "openai/gpt-5.5", "full": "gemini/gemini-4-carbon", "none": "opus", "codex": "gpt-6.1-sol", "fallback": "sonnet"} {
		if got[id] != want {
			t.Errorf("the %s selection names %q after the upgrade, want %q", id, got[id], want)
		}
	}

	first := 1
	if _, err := s.RollbackDelegationPreferences(&first, DelegationPreferencesNote{Message: "back"}); err != nil {
		t.Fatal(err)
	}
	if rolled, err := s.GetDelegationPreferences(); err != nil || rolled.Roles[0].Choices[0].Selection.Model != "openai/gpt-5.4" {
		t.Errorf("rolling back to a revision saved before the upgrade gives %+v (%v), want openai/gpt-5.4", rolled.Roles, err)
	}

	var resolved string
	if err := s.db.QueryRow(`SELECT resolved_preferences FROM delegation_operations WHERE request_id = 'r1'`).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	var operation struct {
		Selection map[string]string `json:"selection"`
	}
	if err := json.Unmarshal([]byte(resolved), &operation); err != nil {
		t.Fatal(err)
	}
	if _, kept := operation.Selection["provider"]; kept || operation.Selection["model"] != "openai/gpt-5.5" {
		t.Errorf("the pending operation's selection is %v after the upgrade, want openai/gpt-5.5 and no provider", operation.Selection)
	}

	var overrides string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'model_tier_overrides' AND copied_from = ''`).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	if want := `[{"harness":"sigma","model":"openai/gpt-5.5","tier":"deep"},{"harness":"claude","model":"opus","tier":"light"},{"harness":"codex","model":"gpt-6.1-sol","tier":"standard"}]`; overrides != want {
		t.Errorf("model_tier_overrides is %s after the upgrade, want %s", overrides, want)
	}
}
