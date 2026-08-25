package pty

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

// fish blocks its prompt redraw until the reattach SIGWINCH's CPR+DA1 are BOTH answered,
// so the daemon answers both from its read loop even while a client is attached.
func TestDaemonAnswersCPRAndDA1FromReadLoop(t *testing.T) {
	const cols, rows = 80, 24

	ptmx, peer := newPollableSocketpair(t)

	gt := newTestGhostty(t, cols, rows)
	s := &Session{
		id:          "cpr",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		child:       &childProcess{cmd: &exec.Cmd{}},
		ghostty:     gt,
		wireFeed:    newWireFeeder(gt, 0, nil, 0),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now().Add(-time.Hour),
	}
	go s.readLoop(nil, func(string, ...any) {})

	s.addSubscriber("frontend", func([]byte, uint32) bool { return true }, nil)

	if _, err := peer.Write([]byte("\x1b[5;7H\x1b[6n\x1b[0c")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return bytes.IndexByte(buf, 'R') >= 0 && bytes.IndexByte(buf, 'c') >= 0
	})

	cpr := regexp.MustCompile(`\x1b\[\d+;\d+R`)
	if !cpr.MatchString(reply) {
		t.Fatalf("daemon did not answer CPR on the PTY; got %q", reply)
	}
	if !bytes.Contains([]byte(reply), []byte("\x1b[5;7R")) {
		t.Fatalf("CPR reply should report the screen cursor \x1b[5;7R; got %q", reply)
	}
	if !bytes.Contains([]byte(reply), []byte("\x1b[?1;2c")) {
		t.Fatalf("daemon should answer DA1 with \x1b[?1;2c; got %q", reply)
	}
}

func TestTerminalQueryRepliesPreserveChunkOrder(t *testing.T) {
	const cols, rows = 80, 24

	ptmx, peer := newPollableSocketpair(t)

	gt := newTestGhostty(t, cols, rows)
	s := &Session{
		id:          "query-order",
		cols:        cols,
		rows:        rows,
		ptmx:        ptmx,
		child:       &childProcess{cmd: &exec.Cmd{}},
		ghostty:     gt,
		wireFeed:    newWireFeeder(gt, 0, nil, 0),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now().Add(-time.Hour),
	}
	go s.readLoop(nil, func(string, ...any) {})

	if _, err := peer.Write([]byte("\x1b[3;4H\x1b[0c\x1b[6n")); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	reply := readReplyUntil(t, peer, 2*time.Second, func(buf []byte) bool {
		return bytes.IndexByte(buf, 'R') >= 0 && bytes.IndexByte(buf, 'c') >= 0
	})

	da1Idx := bytes.Index([]byte(reply), []byte("\x1b[?1;2c"))
	cprIdx := bytes.Index([]byte(reply), []byte("\x1b[3;4R"))
	if da1Idx < 0 || cprIdx < 0 {
		t.Fatalf("expected both DA1 and CPR replies; got %q", reply)
	}
	if da1Idx > cprIdx {
		t.Fatalf("DA1 was asked first but answered second; got %q", reply)
	}
}

func readReplyUntil(t *testing.T, f *os.File, timeout time.Duration, done func([]byte) bool) string {
	t.Helper()
	_ = f.SetReadDeadline(time.Now().Add(timeout))
	var out []byte
	buf := make([]byte, 256)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if done(out) {
				return string(out)
			}
		}
		if err != nil {
			return string(out)
		}
	}
}
