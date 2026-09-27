package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestReadMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(file, []byte("# Hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := readMarkdownFile(file)
	if err != nil || content != "# Hello" {
		t.Fatalf("readMarkdownFile = (%q, %v), want the file body and no error", content, err)
	}
	if _, err := readMarkdownFile(filepath.Join(dir, "missing.md")); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err := readMarkdownFile(dir); err == nil {
		t.Fatal("expected error for directory")
	}
	if _, err := readMarkdownFile(""); err == nil {
		t.Fatal("expected error for empty path")
	}
	fifo := filepath.Join(dir, "doc.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarkdownFile(fifo); err == nil {
		t.Fatal("expected error for fifo")
	}
	tooLarge := filepath.Join(dir, "too-large.md")
	if err := os.WriteFile(tooLarge, make([]byte, maxMarkdownBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarkdownFile(tooLarge); err == nil {
		t.Fatal("expected error for oversized file")
	}
}

func TestOpenMarkdownDocksBesideTheCurrentAgent(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "current.md")
	if err := os.WriteFile(file, []byte("# Current"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenMarkdown(serverConn, &protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Path: file})
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("open_markdown failed: %v", protocol.Deref(resp.Error))
	}
	if tile := desktopTile(t, d, desktop.ID, markdownTileIDForPath(file)); tile.TileParams != file || tile.TileSessionID != "session-1" {
		t.Fatalf("markdown tile = %+v, want the file bound to the current agent", tile)
	}
}

func TestOpenMarkdownForAnUnknownSessionNamesIt(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "remote.md")
	if err := os.WriteFile(file, []byte("# Remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.openMarkdownTile(file, "session-remote"); err == nil || !strings.Contains(err.Error(), "session-remote") {
		t.Fatalf("open for an unknown session = %v, want an error naming it", err)
	}
	after, err := d.store.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leaves := layouttree.TileLeaves(after.Tree); len(leaves) != 0 {
		t.Fatalf("the current desktop gained tiles %+v", leaves)
	}
}

func TestOpenMarkdownWithoutSessionFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenMarkdown(serverConn, &protocol.OpenMarkdownMessage{
		Cmd:  protocol.CmdOpenMarkdown,
		Path: filepath.Join(t.TempDir(), "x.md"),
	})
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Ok {
		t.Fatal("expected failure when no session is selected or provided")
	}
}

func expectOpenMarkdownResult(t *testing.T, client *wsClient) protocol.OpenMarkdownResultMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var msg protocol.OpenMarkdownResultMessage
			if err := json.Unmarshal(outbound.payload, &msg); err != nil || msg.Event != protocol.EventOpenMarkdownResult {
				continue
			}
			return msg
		case <-deadline:
			t.Fatal("timed out waiting for open_markdown_result")
		}
	}
}

func TestOpenMarkdownDocksOneTilePerPath(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	second := filepath.Join(dir, "second.md")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("# Doc"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := d.openMarkdownTile(file, "session-1"); err != nil {
			t.Fatalf("openMarkdownTile(%s): %v", file, err)
		}
	}

	tree := desktopTree(t, d, desktop.ID)
	leaves := layouttree.TileLeaves(tree)
	if len(leaves) != 2 {
		t.Fatalf("tile leaves = %+v, want one tile per open file", leaves)
	}
	byID := make(map[string]layouttree.TileLeaf, len(leaves))
	for _, leaf := range leaves {
		byID[leaf.TileID] = leaf
	}
	for _, file := range []string{first, second} {
		leaf, ok := byID[markdownTileIDForPath(file)]
		if !ok {
			t.Fatalf("no tile docked for %s: %+v", file, leaves)
		}
		if leaf.TileParams != file || leaf.TileSessionID != "session-1" {
			t.Fatalf("leaf for %s = %+v, want path params and session-1 binding", file, leaf)
		}
	}
}

func TestOpenMarkdownNormalizesPathBeforeDerivingTileID(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte("# Notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, spelling := range []string{
		file,
		filepath.Join(dir, ".", "notes.md"),
		dir + "//notes.md",
		filepath.Join(dir, "sub", "..", "notes.md"),
	} {
		if _, tileID, err := d.openMarkdownTile(spelling, "session-1"); err != nil {
			t.Fatalf("openMarkdownTile(%q): %v", spelling, err)
		} else if tileID != markdownTileIDForPath(file) {
			t.Fatalf("openMarkdownTile(%q) tile = %q, want %q", spelling, tileID, markdownTileIDForPath(file))
		}
	}
	tree := desktopTree(t, d, desktop.ID)
	if leaves := layouttree.TileLeaves(tree); len(leaves) != 1 {
		t.Fatalf("tile leaves = %+v, want one tile across all spellings", leaves)
	}

	if _, _, err := d.openMarkdownTile("relative/notes.md", "session-1"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path error = %v, want absolute-path rejection", err)
	}
}

func TestOpenMarkdownReusesATileAlreadyShowingTheFile(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "legacy.md")
	if err := os.WriteFile(file, []byte("# Legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.UpdateDesktopArrangement(desktop.ID, desktop.Revision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		return dockTileOnDesktop(desktop, desktopTileDock{tileID: "tile-markdown", tileKind: string(layouttree.TileKindMarkdown), params: file, edge: protocol.LayoutDockEdgeRight})
	}); err != nil {
		t.Fatalf("dock the existing tile: %v", err)
	}

	gotDesktop, gotTile, err := d.openMarkdownTile(file, "session-1")
	if err != nil {
		t.Fatalf("openMarkdownTile: %v", err)
	}
	if gotDesktop != desktop.ID || gotTile != "tile-markdown" {
		t.Fatalf("open = (%q, %q), want the existing tile reused on %q", gotDesktop, gotTile, desktop.ID)
	}
	if leaves := layouttree.TileLeaves(desktopTree(t, d, desktop.ID)); len(leaves) != 1 {
		t.Fatalf("tile leaves = %+v, want the existing tile only (no hashed duplicate)", leaves)
	}
	if tile := desktopTile(t, d, desktop.ID, "tile-markdown"); tile.TileSessionID != "session-1" {
		t.Fatalf("existing tile binding = %q, want session-1", tile.TileSessionID)
	}
}

func TestOpenMarkdownConcurrentDistinctPathsKeepAllTiles(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	dir := t.TempDir()
	const n = 8
	files := make([]string, n)
	for i := range files {
		files[i] = filepath.Join(dir, fmt.Sprintf("doc-%d.md", i))
		if err := os.WriteFile(files[i], []byte("# Doc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i, file := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = d.openMarkdownTile(file, "session-1")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("openMarkdownTile(%s): %v", files[i], err)
		}
	}

	tree := desktopTree(t, d, desktop.ID)
	leaves := layouttree.TileLeaves(tree)
	if len(leaves) != n {
		t.Fatalf("tile leaves = %d (%+v), want %d — a concurrent dock was lost", len(leaves), leaves, n)
	}
}

func TestOpenMarkdownReusesTileAndRebindsSession(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	injectTestSession(t, d, protocol.Session{ID: "session-2", Label: "second", Directory: t.TempDir()})
	file := filepath.Join(t.TempDir(), "shared.md")
	if err := os.WriteFile(file, []byte("# Shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	tileID := markdownTileIDForPath(file)

	if _, gotTile, err := d.openMarkdownTile(file, "session-1"); err != nil || gotTile != tileID {
		t.Fatalf("first open = (%q, %v), want tile %q", gotTile, err, tileID)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileSessionID != "session-1" {
		t.Fatalf("initial binding = %q, want session-1", tile.TileSessionID)
	}
	layoutAfterFirst := desktopTree(t, d, desktop.ID)

	if _, gotTile, err := d.openMarkdownTile(file, "session-2"); err != nil || gotTile != tileID {
		t.Fatalf("second open = (%q, %v), want reused tile %q", gotTile, err, tileID)
	}
	after := desktopTree(t, d, desktop.ID)
	if leaves := layouttree.TileLeaves(after); len(leaves) != 1 {
		t.Fatalf("tile leaves after reuse = %+v, want exactly one", leaves)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileSessionID != "session-2" {
		t.Fatalf("binding after reuse = %q, want session-2", tile.TileSessionID)
	}
	rebased, ok := layouttree.UpdateTileSessionID(layoutAfterFirst, tileID, "session-2")
	if !ok {
		t.Fatal("rebase helper failed")
	}
	beforeJSON, err := layouttree.EncodeLayout(rebased)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := layouttree.EncodeLayout(after)
	if err != nil {
		t.Fatal(err)
	}
	if beforeJSON != afterJSON {
		t.Fatalf("reuse moved the tile:\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
}

func TestOpenMarkdownWSDocksTileAndReportsResult(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	client := newProtocolTestClient()
	d.wsHub.clients[client] = true
	file := filepath.Join(t.TempDir(), "clicked.md")
	if err := os.WriteFile(file, []byte("# Clicked"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.handleOpenMarkdownWS(client, &protocol.OpenMarkdownMessage{
		Cmd:       protocol.CmdOpenMarkdown,
		Path:      file,
		SessionID: protocol.Ptr("session-1"),
		RequestID: protocol.Ptr("req-1"),
	})

	result := expectOpenMarkdownResult(t, client)
	if !result.Success || result.Error != nil {
		t.Fatalf("result = %+v, want success", result)
	}
	if protocol.Deref(result.RequestID) != "req-1" {
		t.Fatalf("request id = %q, want req-1", protocol.Deref(result.RequestID))
	}
	tileID := markdownTileIDForPath(file)
	if protocol.Deref(result.DesktopID) != desktop.ID || protocol.Deref(result.TileID) != tileID {
		t.Fatalf("result ids = (%q, %q), want (%q, %q)", protocol.Deref(result.DesktopID), protocol.Deref(result.TileID), desktop.ID, tileID)
	}
	if tile := desktopTile(t, d, desktop.ID, tileID); tile.TileSessionID != "session-1" {
		t.Fatalf("binding = %q, want session-1", tile.TileSessionID)
	}
}

func TestOpenMarkdownWSUnknownSessionFails(t *testing.T) {
	d, _ := setupAgentDesktop(t)
	client := newProtocolTestClient()
	d.wsHub.clients[client] = true
	d.handleOpenMarkdownWS(client, &protocol.OpenMarkdownMessage{
		Cmd:       protocol.CmdOpenMarkdown,
		Path:      filepath.Join(t.TempDir(), "x.md"),
		SessionID: protocol.Ptr("session-ghost"),
		RequestID: protocol.Ptr("req-2"),
	})
	result := expectOpenMarkdownResult(t, client)
	if result.Success || result.Error == nil {
		t.Fatalf("result = %+v, want failure for unknown session", result)
	}
	if protocol.Deref(result.RequestID) != "req-2" {
		t.Fatalf("request id = %q, want req-2", protocol.Deref(result.RequestID))
	}
}

func TestOpenMarkdownRecordsRecentFile(t *testing.T) {
	d, _ := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("# Notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, _, err := d.openMarkdownTile(file, "session-1"); err != nil {
			t.Fatalf("openMarkdownTile: %v", err)
		}
	}

	files := d.store.GetRecentFiles(10, "")
	if len(files) != 1 {
		t.Fatalf("recent files = %+v, want one entry", files)
	}
	if files[0].Path != file || files[0].Count != 2 {
		t.Fatalf("recent file = %+v, want %s opened twice", files[0], file)
	}
	if files[0].Source != store.FileActivitySourceOpened {
		t.Fatalf("source = %q, want %q", files[0].Source, store.FileActivitySourceOpened)
	}
}

func TestOpenMarkdownForgetsMissingFile(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("# Notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.openMarkdownTile(file, "session-1"); err != nil {
		t.Fatalf("openMarkdownTile: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	if _, _, err := d.openMarkdownTile(file, "session-1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("openMarkdownTile(deleted) error = %v, want not-found", err)
	}
	if files := d.store.GetRecentFiles(10, ""); len(files) != 0 {
		t.Fatalf("recent files = %+v, want the deleted file forgotten", files)
	}
	tree := desktopTree(t, d, desktop.ID)
	if leaves := layouttree.TileLeaves(tree); len(leaves) != 1 {
		t.Fatalf("tile leaves = %+v, want the existing tile untouched", leaves)
	}
}

func openSentFiles(t *testing.T, d *Daemon, msg *protocol.OpenSentFilesMessage) protocol.Response {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenSentFiles(serverConn, msg)
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestOpenSentFilesRoutesMarkdownAndDropsTheRest(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "plan.md")
	second := filepath.Join(dir, "notes.markdown")
	report := filepath.Join(dir, "report.html")
	for _, f := range []string{first, second, report} {
		if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resp := openSentFiles(t, d, &protocol.OpenSentFilesMessage{
		Cmd:       protocol.CmdOpenSentFiles,
		Paths:     []string{first, second, report},
		SessionID: protocol.Ptr("session-1"),
	})
	if !resp.Ok {
		t.Fatalf("open_sent_files failed: %v", protocol.Deref(resp.Error))
	}

	tree := desktopTree(t, d, desktop.ID)
	for _, f := range []string{first, second} {
		if _, ok := layouttree.TileParamsByID(tree, markdownTileIDForPath(f)); !ok {
			t.Fatalf("markdown %s not docked", f)
		}
	}
	if leaves := layouttree.TileLeaves(tree); len(leaves) != 2 {
		t.Fatalf("tiles = %+v, want exactly the two markdown tiles", leaves)
	}
}

func TestOpenSentFilesDisabledDoesNothing(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	d.store.SetSetting(SettingOpenSentFilesEnabled, "false")
	file := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(file, []byte("# Plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := openSentFiles(t, d, &protocol.OpenSentFilesMessage{
		Cmd:       protocol.CmdOpenSentFiles,
		Paths:     []string{file},
		SessionID: protocol.Ptr("session-1"),
	})
	if !resp.Ok {
		t.Fatalf("disabled open_sent_files must still answer OK, got %v", protocol.Deref(resp.Error))
	}
	if leaves := layouttree.TileLeaves(desktopTree(t, d, desktop.ID)); len(leaves) != 0 {
		t.Fatalf("tiles = %+v, want none while disabled", leaves)
	}
}

func TestOpenSentFilesUnresolvableSessionStaysOK(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	resp := openSentFiles(t, d, &protocol.OpenSentFilesMessage{
		Cmd:   protocol.CmdOpenSentFiles,
		Paths: []string{filepath.Join(t.TempDir(), "x.md")},
	})
	if !resp.Ok {
		t.Fatalf("open_sent_files must never error toward the hook, got %v", protocol.Deref(resp.Error))
	}
}

func TestRedockingATileKeepsItsFraction(t *testing.T) {
	tileID := markdownTileIDForPath("/tmp/README.md")
	desktop := profiles.Desktop{ID: "desktop-1", Tree: layouttree.DefaultLayout("pane-1")}
	docked, err := dockTileOnDesktop(desktop, desktopTileDock{
		tileID: tileID, tileKind: string(layouttree.TileKindMarkdown), params: "/tmp/README.md",
		anchorID: "pane-1", edge: protocol.LayoutDockEdgeRight, share: 0.41,
	})
	if err != nil {
		t.Fatalf("dock: %v", err)
	}
	moved, err := dockTileOnDesktop(docked, desktopTileDock{
		tileID: tileID, tileKind: string(layouttree.TileKindMarkdown), params: "/tmp/README.md",
		anchorID: "pane-1", edge: protocol.LayoutDockEdgeBottom,
	})
	if err != nil {
		t.Fatalf("re-dock: %v", err)
	}
	if got, ok := layouttree.TileFractionByID(moved.Tree, tileID); !ok || got < 0.41-1e-9 || got > 0.41+1e-9 {
		t.Fatalf("tile fraction after moving = (%v, %v), want (0.41, true)", got, ok)
	}
}
