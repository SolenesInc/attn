package pty

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestSessionInfoAndSubscribeDoNotSerializeReplay(t *testing.T) {
	defer func() { infoSnapshotHook = nil }()

	session := &Session{
		id:          "metadata-only",
		agent:       "shell",
		cwd:         t.TempDir(),
		cols:        80,
		rows:        24,
		child:       &childProcess{cmd: &exec.Cmd{}},
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
	}
	manager := NewManager(nil)
	manager.sessions[session.id] = session

	snapshots := 0
	infoSnapshotHook = func() { snapshots++ }

	if _, err := manager.SessionInfo(session.id); err != nil {
		t.Fatalf("SessionInfo() error: %v", err)
	}
	if snapshots != 0 {
		t.Fatalf("SessionInfo() serialized %d replay snapshots, want none", snapshots)
	}
	if _, err := manager.Subscribe(session.id, "observer", func([]byte, uint32) bool { return true }, nil); err != nil {
		t.Fatalf("Subscribe() error: %v", err)
	}
	if snapshots != 0 {
		t.Fatalf("Subscribe() serialized %d replay snapshots, want none", snapshots)
	}
	if _, err := manager.Attach(session.id, "frontend", func([]byte, uint32) bool { return true }, nil); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if snapshots != 1 {
		t.Fatalf("Attach() serialized %d replay snapshots, want one", snapshots)
	}
}

// info() must serialize the snapshot and read lastReplaySeq under one replayMu
// section, or a write in that window is in neither the payload nor the stream.
func TestAttachSnapshotSeqConsistency(t *testing.T) {
	const cols, rows = 80, 24
	defer func() { infoSnapshotHook = nil }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	gt := newTestGhostty(t, cols, rows)
	s := &Session{
		id:          "race",
		cols:        cols,
		rows:        rows,
		ptmx:        r,
		child:       &childProcess{cmd: &exec.Cmd{}}, // unstarted: readLoop's Wait() returns an error, never panics
		ghostty:     gt,
		wireFeed:    newWireFeeder(gt, 0, nil, 0),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	go s.readLoop(nil, func(string, ...any) {})

	mirror := &streamMirror{}
	s.addSubscriber("mirror", mirror.send, nil)

	write := func(line string) int {
		n, werr := w.Write([]byte(line))
		if werr != nil {
			t.Fatalf("pipe write: %v", werr)
		}
		return n
	}
	waitMirror := func(want int) {
		deadline := time.Now().Add(2 * time.Second)
		for mirror.len() < want {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for mirror to reach %d bytes (have %d)", want, mirror.len())
			}
			time.Sleep(100 * time.Microsecond)
		}
	}

	seeded := 0
	for i := range 50 {
		seeded += write(fmt.Sprintf("SEED%05d|prior-command-output-line\n", i))
	}
	waitMirror(seeded)

	lostChunk := "GAP_LOST|command-end+next-prompt\n"
	keptChunk := "GAP_KEPT|following-output\n"
	// Back-to-back writes coalesce into one read and one seq, which would not
	// advance the watermark past the lost chunk.
	injectOneChunk := func(line string) {
		start := s.seqCounter.Load()
		write(line)
		deadline := time.Now().Add(2 * time.Second)
		for s.seqCounter.Load() <= start {
			if time.Now().After(deadline) {
				t.Errorf("injected write %q was not fanned in time", firstLine([]byte(line)))
				return
			}
			time.Sleep(50 * time.Microsecond)
		}
	}
	var once sync.Once
	infoSnapshotHook = func() {
		once.Do(func() {
			injectOneChunk(lostChunk)
			injectOneChunk(keptChunk)
		})
	}

	probe := &recordingSink{}
	s.addSubscriber("probe", probe.send, nil)
	info := s.info()

	boundary := seeded

	var applied []byte
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		applied = probe.appliedAfter(info.LastSeq, 8)
		if len(applied) > 0 {
			break
		}
		time.Sleep(100 * time.Microsecond)
	}
	s.removeSubscriber("probe")
	if len(applied) == 0 {
		t.Fatal("probe never received an applied live chunk")
	}

	waitMirror(boundary + len(applied))
	ok, have := mirror.matchAt(boundary, applied)
	if !have {
		t.Fatal("mirror did not cover the reconstruction boundary")
	}
	if !ok {
		t.Fatalf("re-attach lost output: payload covered %d bytes, LastSeq=%d, but the first applied live bytes are %q, not the next bytes in the stream %q",
			boundary, info.LastSeq, firstLine(applied), firstLine(mirror.slice(boundary, len(applied))))
	}
}

// The read loop allocates a chunk's seq before applying it, so a snapshot in
// that gap must report lastReplaySeq, not seqCounter.
func TestScreenSnapshotSeqConsistency(t *testing.T) {
	const cols, rows = 80, 24
	defer func() { readLoopSeqGapHook = nil }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	gt := newTestGhostty(t, cols, rows)
	s := &Session{
		id:          "snapshot-race",
		cols:        cols,
		rows:        rows,
		ptmx:        r,
		child:       &childProcess{cmd: &exec.Cmd{}}, // unstarted: readLoop's Wait() returns an error, never panics
		ghostty:     gt,
		wireFeed:    newWireFeeder(gt, 0, nil, 0),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	go s.readLoop(nil, func(string, ...any) {})

	mirror := &streamMirror{}
	s.addSubscriber("mirror", mirror.send, nil)

	write := func(line string) int {
		n, werr := w.Write([]byte(line))
		if werr != nil {
			t.Fatalf("pipe write: %v", werr)
		}
		return n
	}
	waitMirror := func(want int) {
		deadline := time.Now().Add(2 * time.Second)
		for mirror.len() < want {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for mirror to reach %d bytes (have %d)", want, mirror.len())
			}
			time.Sleep(100 * time.Microsecond)
		}
	}

	seeded := write("SEED|earlier-output\r\n")
	waitMirror(seeded)

	var (
		once    sync.Once
		gapInfo ScreenSnapshotInfo
		gapSeq  uint32
	)
	readLoopSeqGapHook = func() {
		once.Do(func() {
			gapSeq = s.seqCounter.Load()
			gapInfo = s.screenSnapshot()
		})
	}
	written := write("MARKER|in-flight-chunk\r\n")
	waitMirror(seeded + written)

	if gapSeq == 0 {
		t.Fatal("readLoopSeqGapHook never fired")
	}
	if bytes.Contains(gapInfo.Screen.Payload, []byte("MARKER")) {
		t.Fatal("gap snapshot already contains the in-flight chunk; the seam fired too late to exercise the race")
	}
	if gapInfo.LastSeq >= gapSeq {
		t.Fatalf("snapshot taken before chunk %d reached the screen reports LastSeq=%d — observers deduping seq <= LastSeq would lose the chunk's bytes", gapSeq, gapInfo.LastSeq)
	}

	settled := s.screenSnapshot()
	if settled.LastSeq != gapSeq {
		t.Fatalf("settled snapshot LastSeq = %d, want %d (the applied marker chunk)", settled.LastSeq, gapSeq)
	}
	if !bytes.Contains(settled.Screen.Payload, []byte("MARKER")) {
		t.Fatalf("settled snapshot screen should contain the applied marker chunk; got %q", settled.Screen.Payload)
	}
}

func firstLine(b []byte) string {
	line, _, _ := bytes.Cut(b, []byte{'\n'})
	return string(line)
}

type recordingSink struct {
	mu     sync.Mutex
	chunks []recordedChunk
}

type recordedChunk struct {
	seq  uint32
	data []byte
}

func (r *recordingSink) send(data []byte, seq uint32) bool {
	r.mu.Lock()
	r.chunks = append(r.chunks, recordedChunk{seq: seq, data: append([]byte(nil), data...)})
	r.mu.Unlock()
	return true
}

// Must mirror planLivePtyOutput's stale rule, or the test validates a contract
// no client implements.
func (r *recordingSink) appliedAfter(lastSeq uint32, maxChunks int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []byte
	n := 0
	for _, c := range r.chunks {
		if c.seq > lastSeq {
			out = append(out, c.data...)
			n++
			if n >= maxChunks {
				break
			}
		}
	}
	return out
}

type streamMirror struct {
	mu  sync.Mutex
	buf []byte
}

func (m *streamMirror) send(data []byte, _ uint32) bool {
	m.mu.Lock()
	m.buf = append(m.buf, data...)
	m.mu.Unlock()
	return true
}

func (m *streamMirror) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.buf)
}

// have=false means the mirror has not yet reached offset+len(p).
func (m *streamMirror) matchAt(offset int, p []byte) (ok bool, have bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if offset+len(p) > len(m.buf) {
		return false, false
	}
	return bytes.Equal(m.buf[offset:offset+len(p)], p), true
}

func (m *streamMirror) slice(offset, length int) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := min(offset+length, len(m.buf))
	offset = min(offset, len(m.buf))
	return append([]byte(nil), m.buf[offset:end]...)
}
