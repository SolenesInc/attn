package pty

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

type AttachBlockData struct {
	ID             uint64
	Pending        bool
	PromptRow      int32
	InputRow       *int32
	InputCol       *int32
	OutputStartRow *int32
	EndRow         *int32
	Command        *string
	ExitCode       *int32
}

type workerBlockTable interface {
	ApplyMarker(m osc133Marker, ref blockRef, altScreen bool)
	SnapshotBlocks() []AttachBlockData
	Restore(blocks []AttachBlockData, pin func(x, y int) blockRef)
	Close()
}

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
		if r := f.term.TrackPoint(x, y); r != nil {
			return r
		}
		return nil
	})
}

func (f *blockFeeder) close() {
	f.table.Close()
}
