package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func readDoorbellScreen(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "doorbell", name+".txt"))
	if err != nil {
		t.Fatalf("read screen fixture %s: %v", name, err)
	}
	return string(text)
}

func TestScreenShowsSelector(t *testing.T) {
	for _, name := range []string{"claude-question-selector", "claude-resume-selector"} {
		line, blocked := screenShowsSelector(readDoorbellScreen(t, name))
		if !blocked {
			t.Errorf("%s: the guard did not see a selector", name)
			continue
		}
		if !strings.Contains(strings.ToLower(line), "to select") &&
			!strings.Contains(strings.ToLower(line), "esc to cancel") {
			t.Errorf("%s: named %q as the selector line, which says neither", name, line)
		}
	}

	for _, name := range []string{"claude-composer-working", "claude-composer-idle"} {
		if line, blocked := screenShowsSelector(readDoorbellScreen(t, name)); blocked {
			t.Errorf("%s: a composer was held off as a selector, on %q", name, line)
		}
	}
}

// Receipt for the footer depth is in doorbell_screen.go: on the captured corpus
// 6, 8 and 12 lines find the same screens, and 20 starts matching assistant prose.
func TestScreenShowsSelectorReadsOnlyTheFooter(t *testing.T) {
	scrolled := "Enter to select · Esc to cancel\n" +
		strings.Repeat("a line of ordinary output\n", doorbellScreenTailLines)
	if line, blocked := screenShowsSelector(scrolled); blocked {
		t.Fatalf("a selector that scrolled out of the footer still blocked, on %q", line)
	}

	padded := "Enter to select · Esc to cancel\n\n\n\n\n\n\n\n\n\n"
	if _, blocked := screenShowsSelector(padded); !blocked {
		t.Fatal("blank padding hid a selector footer from the guard")
	}
}

func TestScreenShowsSelectorLeavesProseAlone(t *testing.T) {
	prose := []string{
		"∴ Let me check the AGENTS.md rules to confirm this is a docs-only PR, then start",
		"  I will pick the branch to rebase onto and then open the PR.",
		"  Waiting for you to choose which one to keep.",
	}
	for _, line := range prose {
		if _, blocked := screenShowsSelector(line); blocked {
			t.Errorf("assistant prose was read as a selector: %q", line)
		}
	}
}

func TestScreenShowsSelectorOnAnEmptyScreen(t *testing.T) {
	if _, blocked := screenShowsSelector(""); blocked {
		t.Fatal("an empty viewport was read as a selector")
	}
}

func TestDoorbellHeldOffByAnOnScreenSelector(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	var typed [][]byte
	backend.onInput = func(_ string, data []byte) { typed = append(typed, data) }
	sessionID := "session-selector"
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: sessionID, Label: "member", Agent: protocol.SessionAgentClaude,
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	backend.screen = readDoorbellScreen(t, "claude-question-selector")
	delivery := maintenanceSessionInput("screen-test", "selector", sessionID, "[attn] hand off now", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); !errors.Is(attempt.err, errSessionInputBlockedBySelector) {
		t.Fatalf("typing at a selector returned %v, want errSessionInputBlockedBySelector", attempt.err)
	}
	if len(typed) != 0 {
		t.Fatalf("the doorbell wrote %q at a screen waiting for a keypress", typed)
	}
	backend.screen = readDoorbellScreen(t, "claude-composer-working")
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("typing at a composer failed: %v", attempt.err)
	}
	if len(typed) == 0 {
		t.Fatal("the doorbell wrote nothing at a composer")
	}
}

func TestDoorbellDefersWhenTheScreenIsUnavailable(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	var typed [][]byte
	backend.onInput = func(_ string, data []byte) { typed = append(typed, data) }
	sessionID := "session-no-screen"
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: sessionID, Label: "member", Agent: protocol.SessionAgentClaude,
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	backend.screenUnavailable = true
	delivery := maintenanceSessionInput("screen-test", "unavailable", sessionID, "[attn] hand off now", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); !errors.Is(attempt.err, errSessionInputScreenUnavailable) {
		t.Fatalf("typing without a screen returned %v, want errSessionInputScreenUnavailable", attempt.err)
	}
	if len(typed) != 0 {
		t.Fatal("the boundary wrote input without screen safety evidence")
	}
}
