//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func newT(t *testing.T, cols, rows int) *Terminal {
	t.Helper()
	term, err := New(cols, rows, Options{})
	if err != nil {
		t.Fatalf("New(%d,%d): %v", cols, rows, err)
	}
	t.Cleanup(term.Close)
	return term
}

func restoreT(t *testing.T, snap Snapshot) *Terminal {
	t.Helper()
	term, err := Restore(snap.Payload, Options{})
	if err != nil {
		t.Fatalf("Restore(): %v", err)
	}
	t.Cleanup(term.Close)
	if cols, rows := term.Size(); cols != snap.Cols || rows != snap.Rows {
		t.Fatalf("restored size = %dx%d, want %dx%d", cols, rows, snap.Cols, snap.Rows)
	}
	return term
}

func styledCorpus() []byte {
	var b bytes.Buffer
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "\x1b[3%dmline-%03d\x1b[0m plain tail\r\n", i%7+1, i)
	}
	b.WriteString("\x1b[1;4;35mSTYLED-BOLD-UNDER-MAGENTA\x1b[0m\r\n")
	b.WriteString(strings.Repeat("wrapme-", 30))
	b.WriteString("\r\n")
	b.WriteString("\x1b]8;;https://example.com\x1b\\hyperlinked\x1b]8;;\x1b\\\r\n")
	b.WriteString("final-prompt$ ")
	return b.Bytes()
}

func TestCursorPos(t *testing.T) {
	term := newT(t, 12, 3)
	term.Write([]byte("\x1b[2;7H"))
	if x, y := term.CursorPos(); x != 6 || y != 1 {
		t.Fatalf("CursorPos after CUP = (%d,%d), want (6,1)", x, y)
	}

	for i := 0; i < 8; i++ {
		term.Write([]byte(fmt.Sprintf("line-%d\r\n", i)))
	}
	if x, y := term.CursorPos(); x != 0 || y != 2 {
		t.Fatalf("CursorPos after scrollback = (%d,%d), want viewport-relative (0,2)", x, y)
	}
}

func TestCursorVisible(t *testing.T) {
	term := newT(t, 12, 3)
	if !term.CursorVisible() {
		t.Fatal("fresh cursor is not visible")
	}
	term.Write([]byte("\x1b[?25l"))
	if term.CursorVisible() {
		t.Fatal("cursor remained visible after DECTCEM reset")
	}
	term.Write([]byte("\x1b[?25h"))
	if !term.CursorVisible() {
		t.Fatal("cursor did not become visible after DECTCEM set")
	}
}

func TestViewportText(t *testing.T) {
	term := newT(t, 16, 3)
	term.Write([]byte("scrolled-off-1\r\nscrolled-off-2\r\nvisible-one   \r\nvisible-two\r\nvisible-three"))

	const want = "visible-one\nvisible-two\nvisible-three\n"
	if got := term.ViewportText(); got != want {
		t.Fatalf("ViewportText = %q, want %q", got, want)
	}
}

func TestViewportTextBlankScreenShape(t *testing.T) {
	const rows = 3
	want := strings.Repeat("\n", rows)
	term := newT(t, 10, rows)

	if got := term.ViewportText(); got != want {
		t.Fatalf("fresh ViewportText = %q, want %q", got, want)
	}

	term.Write([]byte("nonblank\x1b[2J\x1b[H"))
	if got := term.ViewportText(); got != want {
		t.Fatalf("cleared ViewportText = %q, want %q", got, want)
	}
}

func TestSerializeViewportRoundTrip(t *testing.T) {
	for _, cursorVisible := range []bool{false, true} {
		t.Run(fmt.Sprintf("cursor_visible_%t", cursorVisible), func(t *testing.T) {
			src := newT(t, 20, 4)
			src.Write([]byte("history-1\r\nhistory-2\r\nhistory-3\r\n\x1b[1;31mBOLD-RED\x1b[0m\r\nvisible-two\r\nvisible-three"))
			src.Write([]byte("\x1b[3;9H"))
			if !cursorVisible {
				src.Write([]byte("\x1b[?25l"))
			}

			snap := src.SerializeViewport()
			if len(snap.VTDump) == 0 {
				t.Fatal("SerializeViewport returned an empty VT dump")
			}
			if !bytes.Contains(snap.VTDump, []byte("\x1b[1m\x1b[38;5;1mBOLD-RED")) {
				t.Fatalf("SerializeViewport lost the bold-red SGR run: %q", snap.VTDump)
			}
			restored := newT(t, snap.Cols, snap.Rows)
			restored.Write(snap.VTDump)

			if got, want := restored.ViewportText(), src.ViewportText(); got != want {
				t.Fatalf("viewport text mismatch after restore\n got: %q\nwant: %q", got, want)
			}
			if gotX, gotY := restored.CursorPos(); gotX != 8 || gotY != 2 {
				t.Fatalf("restored cursor = (%d,%d), want (8,2)", gotX, gotY)
			}
			if got := restored.CursorVisible(); got != cursorVisible {
				t.Fatalf("restored cursor visibility = %t, want %t", got, cursorVisible)
			}
			plain := restored.PlainText()
			if lines := strings.Split(strings.TrimSuffix(plain, "\n"), "\n"); len(lines) != snap.Rows {
				t.Fatalf("restored PlainText has %d rows, want viewport-only %d: %q", len(lines), snap.Rows, plain)
			}
			if got, want := normalizedPlainText(plain, snap.Rows), restored.ViewportText(); got != want {
				t.Fatalf("restored scrollback leaked into PlainText\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

func TestRoundTripPlainText(t *testing.T) {
	a := newT(t, 80, 10)
	a.Write(styledCorpus())

	snap := a.Serialize()
	if len(snap.Payload) == 0 {
		t.Fatal("empty snapshot")
	}

	b := restoreT(t, snap)

	plainA, plainB := a.PlainText(), b.PlainText()
	if plainA != plainB {
		t.Errorf("round-trip plain text mismatch\n A=%q\n B=%q", plainA, plainB)
	}
	if !strings.Contains(plainA, "line-001") {
		t.Errorf("scrollback lost: line-001 not in plain text %q", firstN(plainA, 120))
	}
	if !strings.Contains(plainA, "final-prompt$") {
		t.Errorf("viewport lost: prompt not present")
	}
}

func TestRoundTripCursor(t *testing.T) {
	a := newT(t, 80, 10)
	// Land the cursor at a column that is NOT a tabstop, so the upstream cursor/tabstop
	// ordering bug would move it if uncorrected.
	a.Write([]byte("hello\r\nworld\x1b[3;7H"))
	ax, ay := a.cursorXY()

	b := restoreT(t, a.Serialize())
	bx, by := b.cursorXY()

	if ax != bx || ay != by {
		t.Errorf("cursor position mismatch after restore: A=(%d,%d) B=(%d,%d)", ax, ay, bx, by)
	}
}

func TestReflowAfterRestore(t *testing.T) {
	raw := []byte(strings.Repeat("wrapme-", 30) + "\r\n")
	a := newT(t, 80, 10)
	a.Write(raw)

	b := restoreT(t, a.Serialize())
	b.Resize(40, 10)

	c := newT(t, 80, 10)
	c.Write(raw)
	c.Resize(40, 10)

	if got, want := b.PlainText(), c.PlainText(); got != want {
		t.Errorf("reflow mismatch after resize\n restored=%q\n direct=%q", got, want)
	}
	flat := strings.ReplaceAll(b.PlainText(), "\n", "")
	if !strings.Contains(flat, "wrapme-wrapme-wrapme") {
		t.Errorf("reflow corrupted long line: %q", firstN(flat, 120))
	}
}

func TestRoundTripAltScreen(t *testing.T) {
	a := newT(t, 80, 10)
	a.Write(styledCorpus())
	a.Write([]byte("\x1b[?1049h"))
	a.Write([]byte("\x1b[2J\x1b[HVIM-EDITOR-SCREEN\r\n~\r\n~"))

	snap := a.Serialize()
	if len(snap.Payload) == 0 {
		t.Fatal("empty snapshot")
	}

	b := restoreT(t, snap)

	if got, want := b.PlainText(), a.PlainText(); got != want {
		t.Errorf("alt-active round-trip mismatch\n A=%q\n B=%q", firstN(want, 200), firstN(got, 200))
	}
	if !strings.Contains(b.PlainText(), "VIM-EDITOR-SCREEN") {
		t.Errorf("alt frame lost after restore: %q", firstN(b.PlainText(), 200))
	}
	if strings.Contains(b.PlainText(), "final-prompt$") {
		t.Errorf("primary content leaked into alt viewport: %q", firstN(b.PlainText(), 200))
	}

	a.Write([]byte("\x1b[?1049l"))
	b.Write([]byte("\x1b[?1049l"))
	if got, want := b.PlainText(), a.PlainText(); got != want {
		t.Errorf("primary round-trip mismatch after leaving alt\n A=%q\n B=%q", firstN(want, 240), firstN(got, 240))
	}
	if !strings.Contains(b.PlainText(), "final-prompt$") {
		t.Errorf("primary prompt lost after leaving alt: %q", firstN(b.PlainText(), 240))
	}
	if !strings.Contains(b.PlainText(), "line-001") {
		t.Errorf("primary scrollback lost after leaving alt: %q", firstN(b.PlainText(), 240))
	}
	if strings.Contains(b.PlainText(), "VIM-EDITOR-SCREEN") {
		t.Errorf("alt content leaked into primary after leaving alt: %q", firstN(b.PlainText(), 240))
	}
}

func TestQueryResponses(t *testing.T) {
	term := newT(t, 80, 10)
	term.Write([]byte("\x1b[6n\x1b[c\x1b[?u"))
	resp := string(term.DrainResponses())
	if resp == "" {
		t.Fatal("no query responses drained")
	}
	if !strings.Contains(resp, "R") {
		t.Errorf("CPR not answered: %q", resp)
	}
	if !strings.Contains(resp, "c") {
		t.Errorf("DA1 not answered: %q", resp)
	}
	if !strings.Contains(resp, "u") {
		t.Errorf("kitty CSI ? u not answered: %q", resp)
	}
	if extra := term.DrainResponses(); extra != nil {
		t.Errorf("responses not cleared on drain: %q", string(extra))
	}
}

func TestMalformedInputSafe(t *testing.T) {
	term := newT(t, 80, 10)
	garbage := []byte("\x1b[999;999H\x1b[?xyz\x1b]999;bad\x07\xff\xfe\x1b[38;5;m\x1bP+q\x1b\\partial\x1b[")
	term.Write(garbage)
	// The garbage ends mid-CSI on purpose; a leading ESC in the next write aborts the
	// dangling sequence, so this asserts the parser recovers.
	term.Write([]byte("\x1b[0mrecovered\r\n"))
	if !strings.Contains(term.PlainText(), "recovered") {
		t.Errorf("terminal unusable after garbage input")
	}
}

func TestRestoreAnswersNoQueries(t *testing.T) {
	term := newT(t, 80, 10)
	term.Write(styledCorpus())
	term.Write([]byte("\x1b[6n\x1b[c"))
	if len(term.DrainResponses()) == 0 {
		t.Fatal("source terminal answered no query; the test proves nothing")
	}

	restored := restoreT(t, term.Serialize())
	if got := restored.DrainResponses(); len(got) != 0 {
		t.Errorf("restore produced pty output: %q", got)
	}
}

func TestCloseIdempotent(t *testing.T) {
	term, err := New(20, 5, Options{})
	if err != nil {
		t.Fatal(err)
	}
	term.Write([]byte("hi"))
	term.Close()
	term.Close() // must not panic or double-free
	term.Write([]byte("ignored"))
	if term.PlainText() != "" {
		t.Errorf("PlainText after close should be empty")
	}
}

func (t *Terminal) cursorXY() (x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cursorXYLocked()
}

func normalizedPlainText(s string, rows int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out strings.Builder
	for row := 0; row < rows; row++ {
		line := ""
		if row < len(lines) {
			line = strings.TrimRight(lines[row], " ")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
