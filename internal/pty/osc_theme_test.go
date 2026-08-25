package pty

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func newOSCTestSession(t *testing.T) (s *Session, peer *os.File) {
	t.Helper()
	const cols, rows = 80, 24

	ptmx, peer := newPollableSocketpair(t)

	s = &Session{
		id:          "osc-theme",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		child:       &childProcess{cmd: &exec.Cmd{}},
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now().Add(-time.Hour),
	}
	go s.readLoop(nil, func(string, ...any) {})
	return s, peer
}

func TestOSCColorQuerySingleReply(t *testing.T) {
	_, peer := newOSCTestSession(t)

	if _, err := peer.Write([]byte("\x1b]11;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want exactly %q (one response)", reply, want)
	}
}

func TestOSCColorQuerySetThemeAffectsReply(t *testing.T) {
	s, peer := newOSCTestSession(t)
	s.SetTheme(TerminalTheme{Background: "#ffffff"})

	if _, err := peer.Write([]byte("\x1b]11;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply after SetTheme = %q, want %q", reply, want)
	}
}

func TestOSCColorQuerySeededAtSpawnAnswersWithoutSetTheme(t *testing.T) {
	const cols, rows = 80, 24
	ptmx, peer := newPollableSocketpair(t)

	s := &Session{
		id:          "osc-theme-seeded",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		child:       &childProcess{cmd: &exec.Cmd{}},
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now().Add(-time.Hour),
		theme:       TerminalTheme{Background: "#010203"},
	}
	go s.readLoop(nil, func(string, ...any) {})

	if _, err := peer.Write([]byte("\x1b]11;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]11;rgb:0101/0202/0303\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want %q (seeded theme, no SetTheme call)", reply, want)
	}
}

func TestOSCColorQueryCountsPerChunk(t *testing.T) {
	_, peer := newOSCTestSession(t)

	chunk := "\x1b]11;?\x07\x1b]11;?\x07\x1b]10;?\x07\x1b]11;?\x07"
	if _, err := peer.Write([]byte(chunk)); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	osc10 := "\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\"
	osc11 := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	want := osc11 + osc11 + osc10 + osc11
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want %q (replies in query order, not grouped by code)", reply, want)
	}
}

func TestOSCColorQuerySplitAcrossChunksAnswersOnce(t *testing.T) {
	_, peer := newOSCTestSession(t)

	query := "\x1b]11;?\x07"
	split := len(query) / 2
	if _, err := peer.Write([]byte(query[:split])); err != nil {
		t.Fatalf("peer write (first half): %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := peer.Write([]byte(query[split:])); err != nil {
		t.Fatalf("peer write (second half): %v", err)
	}

	want := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want exactly one response %q", reply, want)
	}

	if got, ok := readAvailable(t, peer, 150*time.Millisecond); ok {
		t.Fatalf("unexpected extra bytes after the single reply: %q", got)
	}
}

func TestOSCColorQuery12AnswersWithCursorColor(t *testing.T) {
	s, peer := newOSCTestSession(t)
	s.SetTheme(TerminalTheme{Cursor: "#abcdef"})

	if _, err := peer.Write([]byte("\x1b]12;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b]12;rgb:abab/cdcd/efef\x1b\\"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestOSCColorSetIsNeverAnswered(t *testing.T) {
	_, peer := newOSCTestSession(t)

	if _, err := peer.Write([]byte("\x1b]11;#000000\x1b\\")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	if got, ok := readAvailable(t, peer, 200*time.Millisecond); ok {
		t.Fatalf("OSC color SET must not be answered; got %q", got)
	}
}

// OSC color replies share the PTY master with Session.input on another goroutine, and
// -race cannot see interleaved writes to an *os.File's fd; assert serialization directly.
func TestOSCColorReplyWaitsForInFlightInput(t *testing.T) {
	s, peer := newOSCTestSession(t)

	// One persistent reader: readAvailable/readReplyUntil each leave an uncancellable
	// goroutine in peer.Read, so a second reader lets the first consume the reply.
	var bufMu sync.Mutex
	var buf []byte
	go func() {
		tmp := make([]byte, 256)
		for {
			n, err := peer.Read(tmp)
			if n > 0 {
				bufMu.Lock()
				buf = append(buf, tmp[:n]...)
				bufMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	snapshot := func() []byte {
		bufMu.Lock()
		defer bufMu.Unlock()
		return append([]byte(nil), buf...)
	}

	inputStarted := make(chan struct{})
	releaseInput := make(chan struct{})
	inputDone := make(chan struct{})
	go func() {
		s.writeMu.Lock()
		close(inputStarted)
		<-releaseInput
		s.writeMu.Unlock()
		close(inputDone)
	}()
	<-inputStarted

	if _, err := peer.Write([]byte("\x1b]11;?\x07")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("OSC reply landed while writeMu was held by another writer: %q", got)
	}

	close(releaseInput)
	<-inputDone

	want := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if string(snapshot()) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("reply after lock release = %q, want %q", snapshot(), want)
}

func readAvailable(t *testing.T, f *os.File, timeout time.Duration) (data []byte, ok bool) {
	t.Helper()
	if err := f.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer func() { _ = f.SetReadDeadline(time.Time{}) }()
	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if n > 0 {
		return buf[:n], true
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("peer read: %v", err)
	}
	return nil, false
}

func TestColorSchemeQueryAnswersFromTheme(t *testing.T) {
	for _, tc := range []struct {
		name       string
		background string
		want       string
	}{
		{name: "default dark theme", background: "", want: "\x1b[?997;1n"},
		{name: "light background", background: "#ffffff", want: "\x1b[?997;2n"},
		{name: "dark background", background: "#1e1e1e", want: "\x1b[?997;1n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, peer := newOSCTestSession(t)
			if tc.background != "" {
				if err := s.SetTheme(TerminalTheme{Background: tc.background}); err != nil {
					t.Fatalf("SetTheme: %v", err)
				}
			}

			if _, err := peer.Write([]byte("\x1b[?996n")); err != nil {
				t.Fatalf("peer write: %v", err)
			}

			reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
				return len(buf) >= len(tc.want)
			})
			if reply != tc.want {
				t.Fatalf("reply = %q, want %q", reply, tc.want)
			}
		})
	}
}

func TestColorSchemeQueryCountsPerChunk(t *testing.T) {
	_, peer := newOSCTestSession(t)

	if _, err := peer.Write([]byte("\x1b[?996n\x1b[?996n")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	want := "\x1b[?997;1n\x1b[?997;1n"
	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return len(buf) >= len(want)
	})
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestColorSchemeReportFollowsMode2031(t *testing.T) {
	t.Run("unsubscribed child hears nothing", func(t *testing.T) {
		s, peer := newOSCTestSession(t)
		if err := s.SetTheme(TerminalTheme{Background: "#ffffff"}); err != nil {
			t.Fatalf("SetTheme: %v", err)
		}
		if got, ok := readAvailable(t, peer, 150*time.Millisecond); ok {
			t.Fatalf("theme change wrote %q to a child that never enabled mode 2031", got)
		}
	})

	t.Run("subscribed child hears the change once", func(t *testing.T) {
		s, peer := newOSCTestSession(t)
		defer func() { colorSchemeReplyHook = nil }()
		themeChanged := make(chan error, 1)
		colorSchemeReplyHook = func() {
			colorSchemeReplyHook = nil
			themeChanged <- s.SetTheme(TerminalTheme{Background: "#ffffff"})
		}

		// The reply makes all preceding bytes observable to the child, so DECSET 2031 must
		// be tracked before the DSR 996 reply is written.
		if _, err := peer.Write([]byte("\x1b[?2031h\x1b[?996n")); err != nil {
			t.Fatalf("peer write: %v", err)
		}
		want := "\x1b[?997;1n\x1b[?997;2n"
		if reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
			return len(buf) >= len(want)
		}); reply != want {
			t.Fatalf("query reply and report after SetTheme = %q, want %q", reply, want)
		}
		if err := <-themeChanged; err != nil {
			t.Fatalf("SetTheme: %v", err)
		}

		if err := s.SetTheme(TerminalTheme{Background: "#fefefe"}); err != nil {
			t.Fatalf("SetTheme: %v", err)
		}
		if got, ok := readAvailable(t, peer, 150*time.Millisecond); ok {
			t.Fatalf("a scheme that did not change still reported %q", got)
		}
	})
}
