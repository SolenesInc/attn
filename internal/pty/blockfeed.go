package pty

// Design: docs/plans/2026-07-23-terminal-restore-fidelity.md.

import "github.com/victorarias/attn/internal/ghosttyvt"

type osc133MarkerKind byte

const (
	osc133PromptStart osc133MarkerKind = 'A'
	osc133InputStart  osc133MarkerKind = 'B'
	osc133PreExec     osc133MarkerKind = 'C'
	osc133CommandEnd  osc133MarkerKind = 'D'
)

type osc133Marker struct {
	Kind     osc133MarkerKind
	Cmdline  *string
	ExitCode *int32
}

type blockRef interface {
	ScreenPoint() (x, y int, ok bool)
	Free()
}

// Rows are SCREEN-space rows of the serialized VT dump.
type AttachBlockData struct {
	ID             uint64
	Pending        bool
	PromptRow      int32
	InputRow       *int32
	InputCol       *int32
	OutputStartRow *int32
	// EndRow is exclusive: the row the next prompt renders on.
	EndRow   *int32
	Command  *string
	ExitCode *int32
}

// Implementations take no locks (calls arrive under replayMu) and must free every
// retired ref. Executable spec: testdata/osc133_block_corpus.json.
type workerBlockTable interface {
	ApplyMarker(m osc133Marker, ref blockRef, altScreen bool)
	SnapshotBlocks() []AttachBlockData
	Restore(blocks []AttachBlockData, pin func(x, y int) blockRef)
	Close()
}

// All methods run under replayMu — what makes the attach snapshot an atomic
// {dump, blocks, watermark} triple.
type blockFeeder struct {
	term  *ghosttyvt.Terminal
	table workerBlockTable
}

func newBlockFeeder(term *ghosttyvt.Terminal) *blockFeeder {
	if term == nil {
		return nil
	}
	return &blockFeeder{term: term, table: newBlockTable()}
}

func (f *blockFeeder) write(segment []byte) {
	if len(segment) > 0 {
		f.term.Write(segment)
	}
}

// Must be called in stream order, after the marker's preceding plain bytes are
// written, so the cursor sits on the cell the pin captures.
func (f *blockFeeder) mark(marker *osc133Marker) {
	if marker == nil {
		return
	}
	var ref blockRef
	if r := f.term.TrackCursor(); r != nil {
		ref = r
	}
	f.table.ApplyMarker(*marker, ref, f.term.AltScreenActive())
}

func (f *blockFeeder) snapshotBlocks() []AttachBlockData {
	return f.table.SnapshotBlocks()
}

func (f *blockFeeder) restore(blocks []AttachBlockData) {
	f.table.Restore(blocks, func(x, y int) blockRef {
		// A nil *TrackedRef must not become a non-nil blockRef holding it.
		if r := f.term.TrackPoint(x, y); r != nil {
			return r
		}
		return nil
	})
}

func (f *blockFeeder) close() {
	f.table.Close()
}
