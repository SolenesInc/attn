//go:build darwin && arm64

package pty

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

func restoredScreenLines(t *testing.T, snap ghosttyvt.Snapshot) []string {
	t.Helper()
	restored, err := ghosttyvt.Restore(snap.Payload, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.Restore: %v", err)
	}
	defer restored.Close()
	lines := strings.Split(restored.PlainText(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

func rowText(lines []string, row int32) string {
	if int(row) < 0 || int(row) >= len(lines) {
		return "<out of range>"
	}
	return lines[row]
}

func TestBlockFeedRoundTrip(t *testing.T) {
	refBase := ghosttyvt.LiveTrackedRefs()

	term, err := ghosttyvt.New(80, 24, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}
	feeder := newWireFeeder(term, 0, nil, 0)
	if feeder == nil {
		t.Fatal("newWireFeeder returned nil for a live terminal")
	}

	feeder.feed([]byte("welcome\r\nto the shell\r\n"))

	feeder.feed([]byte(
		"\x1b]133;A\x07prompt$ \x1b]133;B\x07echo hello\r\n" +
			"\x1b]133;C;cmdline_url=echo%20hello\x07hello\r\n" +
			"\x1b]133;D;0\x07",
	))
	feeder.feed([]byte("\x1b]133;A\x07prompt$ \x1b]133;B\x07make\r\n\x1b]133;C;cmdline_url=ma"))
	feeder.feed([]byte("ke\x07build failed\r\n\x1b]133;D;2\x07"))

	feeder.feed([]byte("\x1b[?1049h"))
	feeder.feed([]byte(
		"\x1b]133;A\x07alt$ \x1b]133;B\x07vimcmd\r\n" +
			"\x1b]133;C;cmdline_url=vimcmd\x07alt output\r\n" +
			"\x1b]133;D;0\x07",
	))
	feeder.feed([]byte("\x1b[?1049l"))

	blocks := feeder.snapshotBlocks()
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (alt-screen block must be excluded): %+v", len(blocks), blocks)
	}

	lines := restoredScreenLines(t, term.Serialize())

	b1 := blocks[0]
	if b1.ID != 1 {
		t.Fatalf("block 1 id = %d, want 1", b1.ID)
	}
	if b1.Pending {
		t.Fatal("block 1 must be completed, not pending")
	}
	if b1.Command == nil || *b1.Command != "echo hello" {
		t.Fatalf("block 1 command = %v, want %q (cmdline_url decoded)", b1.Command, "echo hello")
	}
	if b1.ExitCode == nil || *b1.ExitCode != 0 {
		t.Fatalf("block 1 exit = %v, want 0", b1.ExitCode)
	}
	if got := rowText(lines, b1.PromptRow); !strings.HasPrefix(got, "prompt$") {
		t.Fatalf("block 1 promptRow %d indexes %q, want prompt$*", b1.PromptRow, got)
	}
	if b1.OutputStartRow == nil {
		t.Fatal("block 1 missing outputStartRow")
	}
	if got := rowText(lines, *b1.OutputStartRow); got != "hello" {
		t.Fatalf("block 1 outputStartRow %d indexes %q, want %q", *b1.OutputStartRow, got, "hello")
	}

	b2 := blocks[1]
	if b2.ID <= b1.ID {
		t.Fatalf("block ids not monotonic: %d then %d", b1.ID, b2.ID)
	}
	if b2.Command == nil || *b2.Command != "make" {
		t.Fatalf("block 2 command = %v, want %q", b2.Command, "make")
	}
	if b2.ExitCode == nil || *b2.ExitCode != 2 {
		t.Fatalf("block 2 exit = %v, want 2", b2.ExitCode)
	}
	if b2.OutputStartRow == nil || rowText(lines, *b2.OutputStartRow) != "build failed" {
		got := "<nil>"
		if b2.OutputStartRow != nil {
			got = rowText(lines, *b2.OutputStartRow)
		}
		t.Fatalf("block 2 output row indexes %q, want %q", got, "build failed")
	}
	if b1.EndRow == nil || *b1.EndRow > b2.PromptRow {
		t.Fatalf("block 1 endRow %v should not exceed block 2 promptRow %d", b1.EndRow, b2.PromptRow)
	}

	feeder.close()
	term.Close()
	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked: live=%d baseline=%d", got, refBase)
	}
}
