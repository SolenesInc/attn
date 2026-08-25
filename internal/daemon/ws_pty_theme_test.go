package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

func TestHandleSetTerminalTheme_StoresAndFansOutToLiveSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{sessionIDs: []string{"sess-a", "sess-b"}}
	d.ptyBackend = backend

	palette := []string{
		"#000000", "#cd3131", "#0dbc79", "#e5e510",
		"#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
		"#666666", "#f14c4c", "#23d18b", "#f5f543",
		"#3b8eea", "#d670d6", "#29b8db", "#ffffff",
	}
	d.handleSetTerminalTheme(nil, &protocol.SetTerminalThemeMessage{
		Foreground:  "#aabbcc",
		Background:  "#001122",
		Cursor:      "#334455",
		AnsiPalette: palette,
	})

	want := pty.TerminalTheme{Foreground: "#aabbcc", Background: "#001122", Cursor: "#334455"}
	copy(want.ANSIPalette[:], palette)
	if got := d.currentTerminalTheme(); got != want {
		t.Fatalf("currentTerminalTheme() = %+v, want %+v", got, want)
	}

	backend.mu.Lock()
	gotIDs := append([]string(nil), backend.themeCallIDs...)
	gotThemes := append([]pty.TerminalTheme(nil), backend.themeCalls...)
	backend.mu.Unlock()
	if len(gotIDs) != 2 || gotIDs[0] != "sess-a" || gotIDs[1] != "sess-b" {
		t.Fatalf("SetTheme fan-out ids = %v, want [sess-a sess-b]", gotIDs)
	}
	for _, theme := range gotThemes {
		if theme != want {
			t.Fatalf("SetTheme fan-out theme = %+v, want %+v", theme, want)
		}
	}

	// Driven through the real handler: a hand-built Theme passed to backend.Spawn would
	// assert this test's own code, not handleSpawnSession's "Theme:" line.
	client := newWorkspaceProtocolTestClient()
	spawnForChiefTest(t, d, client, "ws-theme", "sess-c", string(protocol.SessionAgentClaude), false)
	expectSpawnResult(t, client, "sess-c", true)

	opts, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("LastSpawn() ok = false, want true")
	}
	if opts.Theme != want {
		t.Fatalf("spawn Theme = %+v, want %+v", opts.Theme, want)
	}
}

func TestHandleSetTerminalTheme_BlanksInvalidColors(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}

	d.handleSetTerminalTheme(nil, &protocol.SetTerminalThemeMessage{
		Foreground:  "red",
		Background:  "#001122",
		Cursor:      "not-a-color",
		AnsiPalette: []string{"#000000"},
	})

	want := pty.TerminalTheme{Foreground: "", Background: "#001122", Cursor: ""}
	if got := d.currentTerminalTheme(); got != want {
		t.Fatalf("currentTerminalTheme() = %+v, want %+v", got, want)
	}
}
