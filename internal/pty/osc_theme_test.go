package pty

import (
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

func TestOSCColorReplyWaitsForInFlightInput(t *testing.T) {
	s, peer := newOSCTestSession(t)

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
