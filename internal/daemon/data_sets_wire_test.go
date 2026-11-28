package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const sessionsView = `# Sessions

:::data attn="sessions"
:::

:::list turns
title: {name}
:::
`

// sessionGroups is each state group of the view's sessions set with the ids
// of its rows.
func sessionGroups(t *testing.T, view *protocol.ViewDocument) map[string][]string {
	t.Helper()
	raw, shown := viewValues(view)["sessions"]
	if !shown {
		return nil
	}
	var set struct {
		Groups []struct {
			Key  string `json:"key"`
			Rows []struct {
				ID string `json:"id"`
			} `json:"rows"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(raw), &set); err != nil {
		t.Fatalf("the sessions set %s: %v", raw, err)
	}
	groups := map[string][]string{}
	for _, group := range set.Groups {
		for _, row := range group.Rows {
			groups[group.Key] = append(groups[group.Key], row.ID)
		}
	}
	return groups
}

func TestASessionsViewIsSentItsSetOnlyWhenSomethingARowShowsChanges(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1", "s2")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("home")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": sessionsView})

		if _, err := cli.OpenMarkdown(view, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		shown := awaitRunTile(app, view, 0, func(v *protocol.ViewDocument) bool { return sessionGroups(t, v) != nil })
		if got := viewSourceStates(shown.View)["sessions"]; got != "ok " {
			t.Fatalf("the sessions source is %q, want ok", got)
		}
		before := sessionGroups(t, shown.View)
		if !slices.Contains(before["launching"], "s1") || !slices.Contains(before["launching"], "s2") {
			t.Fatalf("the sessions set groups the two sessions as %v, want both launching", before)
		}

		mark := len(app.Log())
		setSetting(t, app, "queue_show_hidden_sessions", "false")
		w.advance(10 * time.Minute)
		if sent := tileContentSentSince(app, view, mark); sent != 0 {
			t.Fatalf("a setting no row shows and ten minutes passing sent the tile %d times, want none", sent)
		}

		mark = len(app.Log())
		if err := cli.UpdateState(protocol.TerminalID(w.Terminal("s2")), protocol.StateWaitingInput); err != nil {
			t.Fatalf("update state: %v", err)
		}
		moved := testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(e protocol.DesktopTileContentMessage) bool {
			return e.Path == view && e.View != nil && slices.Contains(sessionGroups(t, e.View)["waiting"], "s2")
		})
		if got := sessionGroups(t, moved.View)["launching"]; !slices.Equal(got, []string{"s1"}) {
			t.Errorf("after s2 waits for input the launching group is %v, want s1 alone", got)
		}
	})
}

const snoozedView = `# Snoozed

:::data attn="sessions"
:::

:::commands
poke: ./poke.sh {name}
:::

:::list snoozed
title: {name}
controls: poke
:::
`

func TestAViewsOwnCommandRunsFromARowOfAttnsDataSet(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("snoozed")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": snoozedView})
		if err := os.WriteFile(filepath.Join(folder, "poke.sh"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > poked.txt\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.OpenMarkdown(view, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		awaitRunTile(app, view, 0, func(v *protocol.ViewDocument) bool { return sessionGroups(t, v) != nil })

		mark := len(app.Log())
		if err := cli.SnoozeSession("", "s1", time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("snooze: %v", err)
		}
		snoozed := awaitRunTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			var set struct {
				Snoozed []struct {
					ID string `json:"id"`
				} `json:"snoozed"`
			}
			_ = json.Unmarshal([]byte(viewValues(v)["sessions"]), &set)
			return len(set.Snoozed) == 1 && set.Snoozed[0].ID == "s1"
		})
		mark = len(app.Log())
		startView(app, snoozed)
		started := awaitRunTile(app, view, mark, func(v *protocol.ViewDocument) bool {
			return !slices.ContainsFunc(v.Commands, func(c protocol.ViewRunCommand) bool { return c.State != protocol.ViewRunStateStarted })
		})

		mark = len(app.Log())
		if ran := runViewCommandOn(t, app, started, "poke", listLine(t, started.View), "s1"); !ran.Success {
			t.Fatalf("running poke on the snoozed row: %s", protocol.Deref(ran.Error))
		}
		testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return m.Path == view && viewCommandPhase(m, "poke") == "done"
		})
		if got, err := os.ReadFile(filepath.Join(folder, "poked.txt")); err != nil || string(got) != "s1" {
			t.Errorf("poke received %q (%v), want the row's name s1", got, err)
		}
	})
}
