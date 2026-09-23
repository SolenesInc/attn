package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func configuredDelegationPreferences() delegationprefs.Config {
	return delegationprefs.Config{Enabled: true, Roles: []delegationprefs.Role{{ID: "build", Name: "Build", Enabled: true, Description: "Implement changes", Instructions: "Keep {{literal}} intact", DefaultChoiceID: "default", Choices: []delegationprefs.Choice{{ID: "default", Name: "Everyday", Selection: delegationprefs.Selection{Harness: "codex", Model: "test-model", Effort: "medium"}}}}}, Fallback: delegationprefs.Fallback{Selection: delegationprefs.Selection{Harness: "copilot"}}}
}

func TestDelegationPreferencesRoundTripAndDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.db")
	s, err := newSeededStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, err := s.GetDelegationPreferences()
	if err != nil || cfg.Enabled || cfg.Revision != 0 || len(cfg.Roles) != 0 {
		t.Fatalf("fresh config: %+v, %v", cfg, err)
	}
	first, err := s.SaveDelegationPreferences(configuredDelegationPreferences(), DelegationPreferencesNote{})
	if err != nil {
		t.Fatal(err)
	}
	saved := first.Config
	cfg = first.Config
	cfg.Enabled = false
	second, err := s.SaveDelegationPreferences(cfg, DelegationPreferencesNote{})
	if err != nil {
		t.Fatal(err)
	}
	cfg = second.Config
	if cfg.Revision != 2 || !reflect.DeepEqual(saved.Roles, cfg.Roles) {
		t.Fatalf("disable changed saved roles: %+v", cfg)
	}
	other, err := newSeededStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	got, err := other.GetDelegationPreferences()
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatalf("persisted: %+v, %v", got, err)
	}
	if out := delegationprefs.Active(got); len(out.Roles) != 0 || out.Fallback != nil {
		t.Fatalf("disabled configuration leaked: %+v", out)
	}
}

func TestDelegationPreferencesConcurrentEditsRefuseLostUpdate(t *testing.T) {
	s := New()
	defer s.Close()
	saved, err := s.SaveDelegationPreferences(configuredDelegationPreferences(), DelegationPreferencesNote{})
	cfg := saved.Config
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, err := s.SaveDelegationPreferences(cfg, DelegationPreferencesNote{}); results <- err })
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, delegationprefs.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestDelegationPreferencesInvalidEditsDoNotPersist(t *testing.T) {
	s := New()
	defer s.Close()
	for _, mutate := range []func(*delegationprefs.Config){
		func(c *delegationprefs.Config) { c.Roles[0].DefaultChoiceID = "missing" },
		func(c *delegationprefs.Config) { c.Roles = append(c.Roles, c.Roles[0]) },
		func(c *delegationprefs.Config) {
			c.Roles[0].Choices = append(c.Roles[0].Choices, c.Roles[0].Choices[0])
		},
		func(c *delegationprefs.Config) {
			c.Fallback.Selection.Harness = ""
			c.Fallback.Selection.Model = "orphan"
		},
	} {
		c := configuredDelegationPreferences()
		mutate(&c)
		if _, err := s.SaveDelegationPreferences(c, DelegationPreferencesNote{}); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	c, err := s.GetDelegationPreferences()
	if err != nil || c.Revision != 0 {
		t.Fatalf("invalid edit persisted: %+v %v", c, err)
	}
	if _, err := s.db.Exec(`INSERT INTO delegation_preference_revisions(revision,config,created_at) VALUES (1,'broken','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDelegationPreferences(); err == nil {
		t.Fatal("corrupt config silently became defaults")
	}
}

func TestDelegationPreferencesRollbackWalksBackAndRestoresAnyRevision(t *testing.T) {
	s := New()
	defer s.Close()
	names := []string{"First", "Second", "Third"}
	for i, name := range names {
		cfg := configuredDelegationPreferences()
		cfg.Revision = i
		cfg.Roles[0].Name = name
		if _, err := s.SaveDelegationPreferences(cfg, DelegationPreferencesNote{SourceSession: "session-a", Message: "rename to " + name}); err != nil {
			t.Fatal(err)
		}
	}
	roleName := func(r DelegationPreferencesRevision) string {
		if len(r.Config.Roles) == 0 {
			return ""
		}
		return r.Config.Roles[0].Name
	}
	rollback := func(target, expected *int) DelegationPreferencesRevision {
		t.Helper()
		r, err := s.RollbackDelegationPreferences(target, expected, DelegationPreferencesNote{})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	for _, want := range []struct {
		revision, restores int
		name               string
	}{{4, 2, "Second"}, {5, 1, "First"}, {6, 0, ""}} {
		got := rollback(nil, nil)
		if got.Config.Revision != want.revision || *got.Restores != want.restores || roleName(got) != want.name {
			t.Fatalf("bare rollback: want %+v, got revision=%d restores=%d name=%q", want, got.Config.Revision, *got.Restores, roleName(got))
		}
	}
	if _, err := s.RollbackDelegationPreferences(nil, nil, DelegationPreferencesNote{}); err == nil {
		t.Fatal("rolled back past the empty table")
	}

	third := 3
	forward := rollback(&third, nil)
	if forward.Config.Revision != 7 || roleName(forward) != "Third" {
		t.Fatalf("restore forward: %+v", forward)
	}
	stale := 6
	if _, err := s.RollbackDelegationPreferences(nil, &stale, DelegationPreferencesNote{}); !errors.Is(err, delegationprefs.ErrConflict) {
		t.Fatalf("rollback expecting a replaced revision: %v", err)
	}
	if back := rollback(nil, &forward.Config.Revision); back.Config.Revision != 8 || roleName(back) != "" {
		t.Fatalf("bare rollback after a restore returns to what was live before it: %+v", back)
	}

	history, err := s.DelegationPreferencesHistory(3)
	if err != nil || len(history) != 3 {
		t.Fatalf("history: %d %v", len(history), err)
	}
	if history[0].Config.Revision != 8 || history[1].Previous == nil || history[1].Previous.Revision != 6 {
		t.Fatalf("history pairs each revision with the one before it: %+v", history)
	}
	all, err := s.DelegationPreferencesHistory(20)
	if err != nil || len(all) != 8 || all[7].Previous == nil || all[7].Previous.Revision != 0 || all[7].Message != "rename to First" || all[7].SourceSession != "session-a" {
		t.Fatalf("full history: %+v %v", all, err)
	}
}
