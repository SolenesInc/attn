package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilterDiagnosticKind_ExportsOnlyInputRecords(t *testing.T) {
	input := `{"kind":"input","runtimeId":"one","counts":{"keydown:composing":2}}`
	lines := []string{
		`{"kind":"paint","context":{"kind":"input"},"text":"private terminal output"}`,
		input,
		`{"kind":"input","truncated":`,
		`not json`,
	}
	got := filterDiagnosticKind(lines, "input")
	if len(got) != 1 || got[0] != input {
		t.Fatalf("exported %v, want only the complete input record", got)
	}
}

func TestReadLinesFile_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.jsonl")
	_, err := readLinesFile(path)
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error = %q, want it to mention 'no such file'", err.Error())
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to contain the path %q", err.Error(), path)
	}
}

func TestReadLinesFile_LongLine(t *testing.T) {
	longLine := strings.Repeat("x", 200*1024) // 200KiB, well past 64KiB
	path := filepath.Join(t.TempDir(), "long.jsonl")
	content := "short line 1\n" + longLine + "\nshort line 2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	lines, err := readLinesFile(path)
	if err != nil {
		t.Fatalf("readLinesFile: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0] != "short line 1" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if len(lines[1]) != len(longLine) {
		t.Errorf("long line length = %d, want %d (line was truncated)", len(lines[1]), len(longLine))
	}
	if lines[2] != "short line 2" {
		t.Errorf("line 2 = %q", lines[2])
	}
}

func TestReadLinesFile_NoTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notrail.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lines, err := readLinesFile(path)
	if err != nil {
		t.Fatalf("readLinesFile: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(lines) != len(want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestTailLines(t *testing.T) {
	lines := []string{"1", "2", "3", "4", "5"}
	tests := []struct {
		n    int
		want []string
	}{
		{n: 2, want: []string{"4", "5"}},
		{n: 0, want: lines},
		{n: -1, want: lines},
		{n: 100, want: lines},
		{n: 5, want: lines},
	}
	for _, tt := range tests {
		got := tailLines(lines, tt.n)
		if len(got) != len(tt.want) {
			t.Errorf("tailLines(_, %d) = %v, want %v", tt.n, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("tailLines(_, %d)[%d] = %q, want %q", tt.n, i, got[i], tt.want[i])
			}
		}
	}
}

func TestGrepLines(t *testing.T) {
	lines := []string{"apple", "banana", "grape", "pineapple"}

	got, err := grepLines(lines, "")
	if err != nil {
		t.Fatalf("grepLines empty pattern: %v", err)
	}
	if len(got) != len(lines) {
		t.Errorf("empty pattern should be a no-op, got %v", got)
	}

	got, err = grepLines(lines, "an.na")
	if err != nil {
		t.Fatalf("grepLines: %v", err)
	}
	if len(got) != 1 || got[0] != "banana" {
		t.Errorf("grepLines(_, \"an.na\") = %v, want [banana]", got)
	}

	got, err = grepLines(lines, "apple")
	if err != nil {
		t.Fatalf("grepLines: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("grepLines(_, \"apple\") = %v, want 2 matches", got)
	}

	if _, err := grepLines(lines, "("); err == nil {
		t.Error("expected an error for an invalid regexp")
	}
}

func TestFilterSinceLines(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.Local)
	fmtTS := func(t time.Time) string {
		return "[" + t.Format(daemonLogTimestampLayout) + "] INFO: msg"
	}
	lines := []string{
		fmtTS(now.Add(-2 * time.Hour)),
		"  a continuation line of the old one",
		fmtTS(now.Add(-30 * time.Minute)),
		"  a continuation line of the new one",
		fmtTS(now.Add(-1 * time.Minute)),
	}
	cutoff := now.Add(-1 * time.Hour)
	got := filterSinceLines(lines, cutoff)

	want := []string{lines[2], lines[3], lines[4]}
	if len(got) != len(want) {
		t.Fatalf("filterSinceLines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("filterSinceLines[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilterSinceLines_LeadingContinuationExcluded(t *testing.T) {
	lines := []string{"orphan continuation line with no preceding timestamp"}
	got := filterSinceLines(lines, time.Now())
	if len(got) != 0 {
		t.Errorf("filterSinceLines = %v, want empty (no preceding timestamp to inherit)", got)
	}
}
