package daemon

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	// Over 44,724 live viewports, 6, 8 and 12 find the same 47 selector screens
	// and 20 starts matching assistant prose, so 8 is the tripwire.
	doorbellScreenTailLines = 8

	doorbellScreenTimeout = 2 * time.Second
)

// Reads words rather than glyphs on purpose: claude changed which glyphs it
// animates with inside one minor version.
var doorbellSelectorFooter = regexp.MustCompile(`(?i)\bto select\b|\besc to cancel\b`)

func screenShowsSelector(text string) (string, bool) {
	lines := make([]string, 0, doorbellScreenTailLines)
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) > doorbellScreenTailLines {
		lines = lines[len(lines)-doorbellScreenTailLines:]
	}
	for _, line := range lines {
		if doorbellSelectorFooter.MatchString(line) {
			return line, true
		}
	}
	return "", false
}

func (d *Daemon) sessionInputScreen(parent context.Context, sessionID string) (line string, known, selector bool) {
	if d.ptyBackend == nil {
		return "", false, false
	}
	provider, ok := d.ptyBackend.(ptybackend.ScreenSnapshotProvider)
	if !ok {
		return "", false, false
	}
	ctx, cancel := context.WithTimeout(parent, doorbellScreenTimeout)
	defer cancel()
	snapshot, err := provider.ScreenSnapshot(ctx, sessionID)
	if err != nil || snapshot.Screen == nil || !snapshot.Screen.HasText {
		return "", false, false
	}
	line, selector = screenShowsSelector(snapshot.Screen.Text)
	return line, true, selector
}
