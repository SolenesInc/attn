//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestASnapshotLeavesKittyPixelsBehind(t *testing.T) {
	term, err := New(80, 24, Options{KittyImageStorageLimit: 1 << 20})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(term.Close)
	term.Write([]byte("hello\r\n"))
	withoutImage := len(term.Serialize().Payload)

	pixels := base64.StdEncoding.EncodeToString(make([]byte, 4*64*64))
	term.Write([]byte("\x1b_Ga=T,i=7,f=32,s=64,v=64,q=2;" + pixels + "\x1b\\"))
	if _, stored := term.KittyImage(7); !stored {
		t.Fatal("image was not stored")
	}

	snapshot := term.Serialize().Payload
	if len(snapshot) >= withoutImage+4*64*64 {
		t.Fatalf("snapshot is %d bytes with a 16 KiB image, %d without", len(snapshot), withoutImage)
	}
	restored, err := Restore(snapshot, Options{})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(restored.Close)
	if text := restored.PlainText(); !strings.Contains(text, "hello") {
		t.Fatalf("restored text = %q", text)
	}
}
