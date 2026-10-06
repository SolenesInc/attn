package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func TestLegacyUnplacedTerminalSurvivesBindingMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := newStoreAtVersion(path, 169)
	if err != nil {
		t.Fatal(err)
	}
	profile, desktop := mustCreateProfile(t, legacy, "legacy")
	for _, id := range []string{"unplaced", "placed", "closed"} {
		legacyAddSession(t, legacy, id, profile.ID)
	}
	_, _, err = legacyPlaceSession(legacy, SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: desktop.Revision, SessionID: "placed", RuntimeID: "distinct-terminal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(`UPDATE sessions SET closed_at = ? WHERE id = ?`, time.Now().UTC().Format(sortableTimeFormat), "closed"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	check := func(s *Store) {
		t.Helper()
		bindings := make(map[protocol.TerminalID]protocol.SessionID)
		s.OnTerminalBindings(func(rows []TerminalBinding) {
			for _, row := range rows {
				bindings[row.TerminalID] = row.SessionID
			}
		})
		if len(bindings) != 2 || bindings["unplaced"] != "unplaced" || bindings["distinct-terminal"] != "placed" {
			t.Fatalf("migrated bindings = %v, want legacy unplaced and explicit placed identities only", bindings)
		}
	}
	check(current)
	desktop, err = current.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	placed, _, err := current.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: desktop.Revision, SessionID: "unplaced"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pane := range placed.Panes {
		if pane.SessionID == "unplaced" {
			found = true
			if pane.RuntimeID != "unplaced" {
				t.Fatalf("legacy terminal replaced with %s", pane.RuntimeID)
			}
		}
	}
	if !found {
		t.Fatal("legacy session was not placed")
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check(reopened)
}

func TestSeveralLegacyTerminalTilesSurviveBindingMigration(t *testing.T) {
	for _, version := range []int{170, 171} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			legacy, err := newStoreAtVersion(path, version)
			if err != nil {
				t.Fatal(err)
			}
			profile, desktop := mustCreateProfile(t, legacy, "legacy")
			legacyAddSession(t, legacy, "shared", profile.ID)
			if _, _, err := legacyPlaceSession(legacy, SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: desktop.Revision, SessionID: "shared", RuntimeID: "first-terminal"}); err != nil {
				t.Fatal(err)
			}
			// Historical schema permits several tiles; placement commands still create only one.
			if _, err := legacy.db.Exec(`INSERT INTO desktop_panes (pane_id, desktop_id, kind, session_id, runtime_id, title, status, error, created_at, updated_at)
				SELECT 'second-tile', desktop_id, kind, session_id, 'second-terminal', title, status, error, created_at, updated_at
				FROM desktop_panes WHERE session_id = 'shared'`); err != nil {
				t.Fatal(err)
			}
			desktop, err = legacy.GetDesktop(desktop.ID)
			if err != nil {
				t.Fatal(err)
			}
			tree, ok := layouttree.Split(desktop.Tree, desktop.Panes[0].PaneID, "second-tile", "shared-split", layouttree.DirectionVertical, 0.5)
			if !ok {
				t.Fatal("historical fixture could not split the shared session's tile")
			}
			raw, err := json.Marshal(tree)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := legacy.db.Exec(`UPDATE desktops SET tree_json = ? WHERE id = ?`, string(raw), desktop.ID); err != nil {
				t.Fatal(err)
			}
			if err := legacy.Close(); err != nil {
				t.Fatal(err)
			}
			for reopen := 0; reopen < 2; reopen++ {
				current, err := NewWithDB(path)
				if err != nil {
					t.Fatal(err)
				}
				bindings := make(map[protocol.TerminalID]protocol.SessionID)
				current.OnTerminalBindings(func(rows []TerminalBinding) {
					clear(bindings)
					for _, row := range rows {
						bindings[row.TerminalID] = row.SessionID
					}
				})
				if len(bindings) != 2 || bindings["first-terminal"] != "shared" || bindings["second-terminal"] != "shared" {
					t.Fatalf("upgrade/reopen %d bindings = %v, want both terminals showing shared", reopen, bindings)
				}
				if reopen == 0 {
					_, target, err := current.CreateDesktop(profile.ID, "Moved", 0, false)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := current.MoveSessionToDesktop("shared", target.ID, "shared"); err != nil {
						t.Fatal(err)
					}
					if len(bindings) != 2 || bindings["first-terminal"] != "shared" || bindings["second-terminal"] != "shared" {
						t.Fatalf("moving a migrated tile lost a sibling binding: %v", bindings)
					}
				}
				if err := current.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func legacyPlaceSession(s *Store, request SessionPlacementRequest) (profiles.Desktop, string, error) {
	desktop, err := s.GetDesktop(request.DesktopID)
	if err != nil {
		return desktop, "", err
	}
	pane := profiles.Pane{PaneID: "first-tile", DesktopID: desktop.ID, Kind: profiles.PaneKindAgent, SessionID: request.SessionID, RuntimeID: request.RuntimeID, Status: profiles.PaneStatusReady}
	desktop.Panes = append(desktop.Panes, pane)
	desktop.Tree = layouttree.DefaultLayout(pane.PaneID)
	desktop.ActivePaneID = pane.PaneID
	tx, err := s.db.Begin()
	if err != nil {
		return desktop, "", err
	}
	defer tx.Rollback()
	if err := writeDesktopArrangement(tx, time.Now().UTC().Format(sortableTimeFormat), &desktop); err != nil {
		return desktop, "", err
	}
	return desktop, pane.PaneID, tx.Commit()
}

func legacyAddSession(t *testing.T, s *Store, id, profileID string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	if _, err := s.db.Exec(`INSERT INTO sessions (id, label, agent, directory, profile_id, state, state_since, state_updated_at, last_seen)
 VALUES (?, ?, 'codex', '/tmp/project', ?, 'idle', ?, ?, ?)`, id, id, profileID, now, now, now); err != nil {
		t.Fatal(err)
	}
}
