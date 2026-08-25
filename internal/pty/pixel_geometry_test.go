//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// With no pixel geometry to read, emitters guess: measured live in A3, chafa
// assumed ~8 x 11.4 px cells against a real 9 x 22.6 px cell.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	creackpty "github.com/creack/pty"
)

// The grid divides evenly on purpose: a wrong derivation cannot land on the
// right number by rounding.
const (
	geomCols, geomRows   = 40, 12
	geomCellW, geomCellH = 18, 45
	geomXPixel           = geomCols * geomCellW
	geomYPixel           = geomRows * geomCellH
)

const winsizeHelperEnv = "ATTN_PTY_WINSIZE_HELPER"

const winsizeHelperMarker = "attn-winsize"

// This is the re-executed child process, not a test of anything.
func TestPTYWinsizeHelper(t *testing.T) {
	if os.Getenv(winsizeHelperEnv) != "1" {
		t.Skip("helper process for TestResizeReportsPixelGeometryToTheChild")
	}
	// Held until the parent has resized; reporting at spawn time would race it.
	var release string
	if _, err := fmt.Fscanln(os.Stdin, &release); err != nil {
		t.Fatalf("helper never got its release line: %v", err)
	}
	size, err := creackpty.GetsizeFull(os.Stdin)
	if err != nil {
		t.Fatalf("helper TIOCGWINSZ: %v", err)
	}
	fmt.Printf("%s cols=%d rows=%d xpixel=%d ypixel=%d\n",
		winsizeHelperMarker, size.Cols, size.Rows, size.X, size.Y)
}

func newWinsizeHelperSpawn(t *testing.T, id string) *kittySpawn {
	t.Helper()
	m := NewManager(nil)
	t.Cleanup(m.Shutdown)
	spawn := &kittySpawn{
		manager: m,
		id:      id,
		updates: make(chan PlacementUpdate, 16),
		exited:  make(chan struct{}),
		arrived: make(chan struct{}, 1),
	}
	var once sync.Once
	m.SetExitHandler(func(ExitInfo) { once.Do(func() { close(spawn.exited) }) })

	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-winsize",
		ExternalCommand: []string{os.Args[0], "-test.run=^TestPTYWinsizeHelper$"},
		ExternalEnv:     []string{winsizeHelperEnv + "=1"},
		// Deliberately not the geometry under test: the resize has to be what
		// puts the pixels there.
		Cols: 20,
		Rows: 6,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	if _, err := m.Attach(id, "test-client", func(data []byte, _ uint32) bool {
		spawn.mu.Lock()
		spawn.output = append(spawn.output, data...)
		spawn.mu.Unlock()
		select {
		case spawn.arrived <- struct{}{}:
		default:
		}
		return true
	}, nil); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	return spawn
}

func TestResizeReportsPixelGeometryToTheChild(t *testing.T) {
	spawn := newWinsizeHelperSpawn(t, "winsize")

	if _, err := spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}
	if err := spawn.manager.Input(spawn.id, []byte("go\n")); err != nil {
		t.Fatalf("releasing the helper: %v", err)
	}
	spawn.waitForOutput(t, winsizeHelperMarker)

	want := fmt.Sprintf("%s cols=%d rows=%d xpixel=%d ypixel=%d",
		winsizeHelperMarker, geomCols, geomRows, geomXPixel, geomYPixel)
	spawn.mu.Lock()
	got := string(spawn.output)
	spawn.mu.Unlock()
	if !strings.Contains(got, want) {
		t.Fatalf("the child read %q from TIOCGWINSZ, want a line containing %q", firstLines(got), want)
	}
}

// In-band reports (DEC mode 2048) are the only answer to check: ghostty's VT
// core does not implement XTWINOPS, so there is no CSI 14 t.
func TestResizeDerivesTheWorkerCellFromThePaneTotal(t *testing.T) {
	spawn := newQuietSpawn(t, "worker-cell", 20, 6)
	term := sessionTerminal(t, spawn)
	term.Write([]byte("\x1b[?2048h"))
	term.DrainResponses()

	if _, err := spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}

	// The 8x16 placeholder this replaced would report 192;320 at this grid.
	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", geomRows, geomCols, geomYPixel, geomXPixel)
	if got := string(term.DrainResponses()); !strings.Contains(got, want) {
		t.Fatalf("the worker terminal reported %q after a resize carrying %dx%d pixels, want %q",
			got, geomXPixel, geomYPixel, want)
	}
}

// The attach-time reconcile and the remount hydrate resize carry no pixels and
// arrive after a fit, so "no pixels" must not blank the remembered cell.
func TestPixelLessResizeKeepsTheCellItAlreadyHas(t *testing.T) {
	spawn := newQuietSpawn(t, "pixel-less", geomCols, geomRows)
	term := sessionTerminal(t, spawn)

	if _, err := spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel); err != nil {
		t.Fatalf("Resize() carrying pixels: %v", err)
	}
	term.Write([]byte("\x1b[?2048h"))
	term.DrainResponses()

	const narrowCols = 30
	if _, err := spawn.manager.Resize(spawn.id, narrowCols, geomRows, 0, 0); err != nil {
		t.Fatalf("Resize() without pixels: %v", err)
	}

	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", geomRows, narrowCols, geomYPixel, narrowCols*geomCellW)
	if got := string(term.DrainResponses()); !strings.Contains(got, want) {
		t.Fatalf("the worker terminal reported %q after a pixel-less resize, want %q", got, want)
	}

	session, err := spawn.manager.getSession(spawn.id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	size, err := creackpty.GetsizeFull(session.ptmx)
	if err != nil {
		t.Fatalf("TIOCGWINSZ: %v", err)
	}
	if size.X != narrowCols*geomCellW || size.Y != geomYPixel {
		t.Fatalf("the kernel winsize is %dx%d pixels after a pixel-less resize, want %dx%d",
			size.X, size.Y, narrowCols*geomCellW, geomYPixel)
	}
}

func TestResizeDeduplicatesAppliedGeometry(t *testing.T) {
	spawn := newQuietSpawn(t, "resize-noop", geomCols, geomRows)

	changed, err := spawn.manager.Resize(spawn.id, geomCols, geomRows, 0, 0)
	if err != nil {
		t.Fatalf("initial pixel-less Resize() error: %v", err)
	}
	if changed {
		t.Fatal("the spawn geometry was reported as changed")
	}

	changed, err = spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel)
	if err != nil {
		t.Fatalf("measured Resize() error: %v", err)
	}
	if !changed {
		t.Fatal("the first measured geometry was reported as unchanged")
	}

	changed, err = spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel, geomYPixel)
	if err != nil {
		t.Fatalf("repeated measured Resize() error: %v", err)
	}
	if changed {
		t.Fatal("identical measured geometry was reported as changed")
	}

	changed, err = spawn.manager.Resize(spawn.id, geomCols, geomRows, 0, 0)
	if err != nil {
		t.Fatalf("pixel-less reconcile Resize() error: %v", err)
	}
	if changed {
		t.Fatal("a pixel-less reconcile that derives the applied totals was reported as changed")
	}

	changed, err = spawn.manager.Resize(spawn.id, geomCols, geomRows, geomXPixel+1, geomYPixel)
	if err != nil {
		t.Fatalf("same-grid pixel Resize() error: %v", err)
	}
	if !changed {
		t.Fatal("a new exact pixel total was reported as unchanged")
	}

	changed, err = spawn.manager.Resize(spawn.id, geomCols, geomRows, 0, 0)
	if err != nil {
		t.Fatalf("pixel-less reconcile after a remainder Resize() error: %v", err)
	}
	if changed {
		t.Fatal("a pixel-less same-grid reconcile discarded the applied pixel remainder")
	}
}

func TestSpawnIsPixelLess(t *testing.T) {
	spawn := newKittySpawnCmd(t, "spawn-pixels", "", "read hold # %s")
	session, err := spawn.manager.getSession(spawn.id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	size, err := creackpty.GetsizeFull(session.ptmx)
	if err != nil {
		t.Fatalf("TIOCGWINSZ: %v", err)
	}
	if size.X != 0 || size.Y != 0 {
		t.Fatalf("a fresh session reports %dx%d winsize pixels, want none until a client measures the pane", size.X, size.Y)
	}
}

func firstLines(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
