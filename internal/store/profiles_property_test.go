package store

import (
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func TestArrangementInvariantsHoldUnderRandomOperations(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := New()
		defer s.Close()
		profile, _, err := s.CreateProfile("Main")
		if err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}
		other, _, err := s.CreateProfile("Other")
		if err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}
		sessionIDs := make([]string, 6)
		for i := range sessionIDs {
			sessionIDs[i] = fmt.Sprintf("agent-%d", i)
			owner := profile.ID
			if i == len(sessionIDs)-1 {
				owner = other.ID
			}
			now := string(protocol.TimestampNow())
			if err := s.AddChecked(&protocol.Session{
				ID: protocol.SessionID(sessionIDs[i]), Label: sessionIDs[i], Agent: protocol.SessionAgentCodex, Directory: "/tmp/project", ProfileID: owner,
				State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
			}); err != nil {
				t.Fatalf("AddChecked: %v", err)
			}
		}
		refusal := func(err error) {
			var profileErr *profiles.Error
			if err != nil && !errors.As(err, &profileErr) {
				t.Fatalf("operation failed with an untyped error: %v", err)
			}
		}
		draw := func(label string) profiles.Desktop {
			_, live, err := s.ProfileArrangement(profile.ID)
			if err != nil {
				t.Fatal(err)
			}
			desktop, err := s.GetDesktop(rapid.SampledFrom(live).Draw(t, label).ID)
			if err != nil {
				t.Fatalf("GetDesktop: %v", err)
			}
			return desktop
		}
		leaves := func(desktop profiles.Desktop) []string {
			return append(layouttree.PaneIDs(desktop.Tree), layouttree.TileIDs(desktop.Tree)...)
		}
		directions := []layouttree.Direction{layouttree.DirectionVertical, layouttree.DirectionHorizontal}
		tiles := 0

		t.Repeat(map[string]func(*rapid.T){
			"create": func(t *rapid.T) { _, _, err := s.CreateDesktop(profile.ID, "", 0, true); refusal(err) },
			"place": func(t *rapid.T) {
				desktop := draw("desktop")
				_, _, err := s.PlaceSession(SessionPlacementRequest{
					DesktopID: desktop.ID,
					SessionID: protocol.SessionID(rapid.SampledFrom(sessionIDs).Draw(t, "session")),
					Direction: rapid.SampledFrom(directions).Draw(t, "direction"), NewPaneShare: rapid.Float64Range(-0.5, 1.5).Draw(t, "share"),
				})
				refusal(err)
			},
			"dock_tile": func(t *rapid.T) {
				desktop := draw("desktop")
				anchors := leaves(desktop)
				if len(anchors) == 0 {
					t.Skip("nothing to dock beside")
				}
				tiles++
				tileID := fmt.Sprintf("tile-%d", tiles)
				_, err := s.EditDesktopArrangement(desktop.ID, func(d profiles.Desktop) (profiles.Desktop, error) {
					next, ok := layouttree.DockTile(d.Tree, rapid.SampledFrom(anchors).Draw(t, "anchor"), rapid.SampledFrom(directions).Draw(t, "direction"),
						rapid.Bool().Draw(t, "before"), "split-"+tileID, tileID, string(layouttree.TileKindMarkdown), "{}", "", 0.4)
					if !ok {
						return d, profiles.Errorf(profiles.CodeInvalid, "tile did not dock")
					}
					d.Tree = next
					return d, nil
				})
				refusal(err)
			},
			"remove": func(t *rapid.T) {
				desktop := draw("desktop")
				candidates := append(leaves(desktop), "missing-leaf")
				_, err := s.RemoveLeaf(desktop.ID, rapid.SampledFrom(candidates).Draw(t, "leaf"))
				refusal(err)
			},
			"move": func(t *rapid.T) {
				source, target := draw("source"), draw("target")
				candidates := leaves(source)
				if len(candidates) == 0 {
					t.Skip("nothing to move")
				}
				leaf := rapid.SampledFrom(candidates).Draw(t, "leaf")
				anchor := rapid.SampledFrom(append([]string{""}, leaves(target)...)).Draw(t, "anchor")
				_, err := s.MoveLeaf(LeafMoveRequest{
					SourceDesktopID: source.ID, TargetDesktopID: target.ID, LeafID: leaf, AnchorID: anchor,
					Direction: rapid.SampledFrom(directions).Draw(t, "direction"), Before: rapid.Bool().Draw(t, "before"),
					LeafShare: rapid.Float64Range(-0.5, 1.5).Draw(t, "share"),
				})
				refusal(err)
			},
			"focus": func(t *rapid.T) {
				desktop := draw("desktop")
				_, _, err := s.SetActivePane(desktop.ID, rapid.SampledFrom(append(layouttree.PaneIDs(desktop.Tree), "missing-pane")).Draw(t, "pane"))
				refusal(err)
				_, err = s.SetCurrentDesktop(profile.ID, desktop.ID)
				refusal(err)
			},
			"": func(t *rapid.T) {
				current, desktops, err := s.ProfileArrangement(profile.ID)
				if err != nil {
					t.Fatalf("ProfileArrangement: %v", err)
				}
				placedOn := make(map[string]string)
				hasCurrent := false
				for _, desktop := range desktops {
					hasCurrent = hasCurrent || desktop.ID == current.CurrentDesktopID
					if err := profiles.CheckDesktop(desktop); err != nil {
						t.Fatalf("stored desktop broke an invariant: %v", err)
					}
					for _, pane := range desktop.Panes {
						if where, dup := placedOn[string(pane.SessionID)]; dup {
							t.Fatalf("session %s is placed on %s and %s", pane.SessionID, where, desktop.ID)
						}
						placedOn[string(pane.SessionID)] = desktop.ID
						if owner, _ := s.SessionProfileID(pane.SessionID); owner != desktop.ProfileID {
							t.Fatalf("session %s of profile %s is placed on a desktop of profile %s", pane.SessionID, owner, desktop.ProfileID)
						}
					}
				}
				if !hasCurrent {
					t.Fatalf("current desktop %q is not a desktop of the profile", current.CurrentDesktopID)
				}
			},
		})
	})
}
