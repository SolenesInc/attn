//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Decoded by the browser binding in app/src/ghostty/terminal.snapshot.test.ts: one committed artifact pins the native encoder and the wasm decoder to one format.
const fixturePath = "../../app/src/ghostty/testdata/native-snapshot.bin"

func fixtureCorpus() []byte {
	var b bytes.Buffer
	for i := 1; i <= 1200; i++ {
		fmt.Fprintf(&b, "\x1b[3%dmrow-%04d\x1b[0m tail\r\n", i%7+1, i)
	}
	b.WriteString("\x1b[1;4;35mSTYLED\x1b[0m\r\n")
	b.WriteString(strings.Repeat("wrap", 15))
	b.WriteString("\r\nprompt$ ")
	b.WriteString("\x1b[38;2;10")
	return b.Bytes()
}

func fixtureTerminal(t *testing.T) *Terminal {
	t.Helper()
	term, err := New(40, 6, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(term.Close)
	term.Write(fixtureCorpus())
	return term
}

// Regenerate with ATTN_UPDATE_FIXTURES=1 after a pin bump.
func TestSnapshotFixtureIsCurrent(t *testing.T) {
	snap := fixtureTerminal(t).Serialize()

	if os.Getenv("ATTN_UPDATE_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(fixturePath, snap.Payload, 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		t.Logf("wrote %s (%d bytes)", fixturePath, len(snap.Payload))
	}

	payload, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture (regenerate with ATTN_UPDATE_FIXTURES=1): %v", err)
	}
	term, err := Restore(payload, Options{})
	if err != nil {
		t.Fatalf("Restore(fixture) on this pin: %v", err)
	}
	t.Cleanup(term.Close)

	if cols, rows := term.Size(); cols != 40 || rows != 6 {
		t.Fatalf("fixture size = %dx%d, want 40x6", cols, rows)
	}
	want := fixtureTerminal(t).ViewportText()
	if got := term.ViewportText(); got != want {
		t.Fatalf("fixture viewport drifted from the corpus that produced it:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
