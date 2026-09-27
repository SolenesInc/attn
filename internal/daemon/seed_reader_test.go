package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestOpenSeedDocksBesideTheCallerAndBindsItsTender(t *testing.T) {
	d := newGardenDaemon(t)
	_, desktop := setupAgentDesktopOn(t, d)
	injectTestSession(t, d, protocol.Session{ID: "sess-a", Label: "tender", Directory: t.TempDir()})
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Read me"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")

	gotDesktop, tileID, err := d.openSeedTile(seed.ID, "session-1", false)
	if err != nil {
		t.Fatalf("openSeedTile: %v", err)
	}
	if gotDesktop != desktop.ID || tileID != seedTileIDForID(seed.ID) {
		t.Fatalf("open = (%q, %q), want (%q, %q)", gotDesktop, tileID, desktop.ID, seedTileIDForID(seed.ID))
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileKind != string(layouttree.TileKindSeed) || tile.TileParams != seed.ID || tile.TileSessionID != "sess-a" {
		t.Fatalf("seed tile = %+v, want seed params and tender binding", tile)
	}
	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "")
	if _, reopenedTileID, err := d.openSeedTile(seed.ID, "session-1", false); err != nil || reopenedTileID != tileID {
		t.Fatalf("reopen = (%q, %v), want existing %q", reopenedTileID, err, tileID)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileSessionID != "session-1" {
		t.Fatalf("reopened seed tile = %+v, want the caller's binding once the tender let go", tile)
	}
}

func TestAStandaloneSeedOpensOnTheCurrentDesktopWithoutBindingTheFocusedAgent(t *testing.T) {
	d := newGardenDaemon(t)
	_, desktop := setupAgentDesktopOn(t, d)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Read from Crew"})

	gotDesktop, tileID, err := d.openSeedTile(seed.ID, "", true)
	if err != nil {
		t.Fatalf("standalone open: %v", err)
	}
	if gotDesktop != desktop.ID {
		t.Fatalf("standalone seed opened on %q, want the current desktop %s", gotDesktop, desktop.ID)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileParams != seed.ID || tile.TileSessionID != "" {
		t.Fatalf("standalone seed tile = %+v, want the seed and no agent binding", tile)
	}
	if again, againTile, err := d.openSeedTile(seed.ID, "", true); err != nil || again != desktop.ID || againTile != tileID {
		t.Fatalf("second standalone open = (%q, %q, %v), want the same tile", again, againTile, err)
	}

	if _, _, err := d.openSeedTile(seed.ID, "session-1", false); err != nil {
		t.Fatal(err)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileSessionID != "session-1" {
		t.Fatalf("seed opened by session-1 bound %q, want session-1", tile.TileSessionID)
	}
	if _, _, err := d.openSeedTile(seed.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileSessionID != "" {
		t.Fatalf("standalone reopen kept the binding %q, want none", tile.TileSessionID)
	}
}

func TestReopeningASeedResetsItsTileNavigatedToAnotherSeed(t *testing.T) {
	d := newGardenDaemon(t)
	_, desktop := setupAgentDesktopOn(t, d)
	first := plant(t, d, protocol.SeedPlantMessage{Title: "First"})
	second := plant(t, d, protocol.SeedPlantMessage{Title: "Second"})
	_, tileID, err := d.openSeedTile(first.ID, "", true)
	if err != nil {
		t.Fatalf("open first seed: %v", err)
	}
	current, err := d.store.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.UpdateDesktopArrangement(desktop.ID, current.Revision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		return applyDesktopTileUpdate(desktop, desktopTileUpdate{tileID: tileID, params: second.ID, hasParams: true})
	}); err != nil {
		t.Fatal(err)
	}

	if _, reopenedTileID, err := d.openSeedTile(first.ID, "", true); err != nil || reopenedTileID != tileID {
		t.Fatalf("reopen first seed = (%q, %v), want %q", reopenedTileID, err, tileID)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileParams != first.ID {
		t.Fatalf("reopened seed tile = %+v, want params %s", tile, first.ID)
	}
}

func TestOpenSeedWSStandaloneDoesNotBindTheFocusedAgent(t *testing.T) {
	d := newGardenDaemon(t)
	_, desktop := setupAgentDesktopOn(t, d)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Open for an asleep member"})
	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleOpenSeedWS(client, &protocol.OpenSeedMessage{
		Cmd: protocol.CmdOpenSeed, SeedID: seed.ID, Standalone: protocol.Ptr(true), RequestID: protocol.Ptr("open-standalone"),
	})
	var result protocol.OpenSeedResultMessage
	message := <-client.send
	if err := json.Unmarshal(message.payload, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Success || protocol.Deref(result.DesktopID) != desktop.ID {
		t.Fatalf("standalone open_seed_result = %+v, want the current desktop %s", result, desktop.ID)
	}
	if tile := desktopTile(t, d, desktop.ID, protocol.Deref(result.TileID)); tile.TileSessionID != "" {
		t.Fatalf("standalone seed tile bound %q, want no agent", tile.TileSessionID)
	}
}

func TestOpeningASeedWhoseTenderClosedBindsTheOpener(t *testing.T) {
	d := newGardenDaemon(t)
	_, desktop := setupAgentDesktopOn(t, d)
	injectTestSession(t, d, protocol.Session{ID: "sess-a", Label: "tender", Directory: t.TempDir()})
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Left growing"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	if _, err := d.store.CloseSession("sess-a", store.SessionClose{}, time.Now()); err != nil {
		t.Fatal(err)
	}

	_, tileID, err := d.openSeedTile(seed.ID, "session-1", false)
	if err != nil {
		t.Fatalf("open a seed whose tender closed: %v", err)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileSessionID != "session-1" {
		t.Fatalf("seed tile bound %q, want the opener session-1", tile.TileSessionID)
	}
	if _, _, err := d.openSeedTile(seed.ID, "", true); err != nil {
		t.Fatalf("standalone open of a seed whose tender closed: %v", err)
	}
}
