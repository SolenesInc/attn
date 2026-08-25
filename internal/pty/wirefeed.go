package pty

// Design: docs/plans/2026-08-02-terminal-kitty-images.md.

import (
	"bytes"
	"strconv"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

const (
	kittyResyncAnchorLost        = "kitty_layout_anchor_lost"
	kittyResyncAnchorClamped     = "kitty_layout_anchor_clamped"
	kittyResyncReverseScroll     = "kitty_layout_reverse_scroll"
	kittyResyncUndescribedImage  = "kitty_undescribed_image"
	kittyResyncStampWithoutDelta = "kitty_stamp_without_delta"
	// DECLRMM (DEC mode 69) was on: a margin-box scroll moves text without moving rows, so
	// no SU goes out. Measured at da5ddcb; a tripwire — no A4-sweep emitter enables DECLRMM.
	kittyResyncMarginMode = "kitty_layout_margin_mode"
	// The placement scrolled further than one SU can express, so the client's history would
	// come out short. Re-probed at da5ddcb over 645 shapes: none reached this.
	kittyResyncScrollClamped = "kitty_layout_scroll_clamped"
	// The cursor sat in the LAST COLUMN, where a dispatch may consume a pending-wrap bit
	// CursorPos cannot see (measured at d760ee9; gone at da5ddcb over 336 shapes).
	kittyResyncPendingWrap = "kitty_layout_pending_wrap"
)

type kittyPlacementKey struct {
	ImageID     uint32
	PlacementID uint32
}

type kittyPlacementDelta struct {
	Added   []ghosttyvt.KittyPlacement
	Removed []kittyPlacementKey
	Updated []ghosttyvt.KittyPlacement
}

func (d kittyPlacementDelta) empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Updated) == 0
}

// wireFeeder splits PTY output into plain runs and kitty APCs, feeds all of it to the
// terminal, and returns what the wire carries instead. Every method is called under replayMu.
type wireFeeder struct {
	term   *ghosttyvt.Terminal
	blocks *blockFeeder
	seg    feedSegmenter

	// Assembly buffer, reused across calls; the slice feed hands out is valid
	// only until the next feed.
	wire []byte

	// Ghostty's kitty stamp as of the last change this feeder ACCOUNTED for; a difference
	// against the terminal's own stamp is exactly the undescribed kind. Raw, no epoch.
	generation uint64

	// Offset folded into every generation handed out; must match
	// Session.kittyEpoch. See mintKittyEpoch.
	epoch uint64

	placements []ghosttyvt.KittyPlacement
	deltas     []kittyPlacementDelta

	resync string

	logf LogFunc

	kittyLimit uint64

	pending kittyTransmission
}

func newWireFeeder(term *ghosttyvt.Terminal, epoch uint64, logf LogFunc, kittyLimit uint64) *wireFeeder {
	blocks := newBlockFeeder(term)
	if blocks == nil {
		return nil
	}
	return &wireFeeder{
		term:       term,
		blocks:     blocks,
		epoch:      epoch,
		logf:       logf,
		kittyLimit: kittyLimit,
		generation: term.KittyGeneration(),
	}
}

// The returned slice is the INPUT slice when no rewriting was needed, otherwise the
// feeder's assembly buffer, valid until the next feed. Empty means the chunk was held.
func (f *wireFeeder) feed(data []byte) ([]byte, string) {
	f.deltas = f.deltas[:0]
	f.resync = ""
	f.wire = f.wire[:0]
	if len(data) == 0 {
		return nil, ""
	}

	// Every emitted slice other than the passthrough case aliases a buffer the
	// segmenter may rewrite before Feed returns, so it is copied on the spot.
	whole := false
	first := true
	f.seg.Feed(data, func(seg feedSegment) {
		switch seg.Kind {
		case feedSegPlain:
			f.blocks.write(seg.Bytes)
			if first && len(seg.Bytes) == len(data) && &seg.Bytes[0] == &data[0] {
				whole = true
			} else {
				f.wire = append(f.wire, seg.Bytes...)
			}
		case feedSegKittyAPC:
			f.writeAPC(seg.Bytes)
		case feedSegOSC133:
			// Write before mark() so the block-table pin lands on Ghostty's
			// post-marker cursor.
			f.wire = append(f.wire, seg.Bytes...)
			f.blocks.write(seg.Bytes)
			f.blocks.mark(seg.Marker)
		}
		first = false
	})

	settled := f.settleUnaccounted()

	if !settled && len(f.placements) > 0 {
		f.observe()
	}

	if whole {
		return data, f.resync
	}
	return f.wire, f.resync
}

// wireST is always 7-bit: a raw 0x9c is a stray UTF-8 continuation byte to the client.
// Wherever the two streams differ, BOTH sides get an ESC-led no-op at that position.
var wireST = []byte{0x1b, '\\'}

// writeAPC feeds one complete kitty APC to the terminal and appends what the wire needs.
// Ordering is the contract: end the pending decode on both sides BEFORE anything is measured.
func (f *wireFeeder) writeAPC(apc []byte) {
	f.settleUnaccounted()

	// Ending a decode is a GRID event (a replacement character on the last column commits
	// the deferred wrap), so it happens before the pin. From ground ghostty treats ST as a no-op.
	f.term.Write(wireST)
	f.wire = append(f.wire, wireST...)

	generation := f.generation
	col, row := f.term.CursorPos()
	before := f.term.TrackCursor()

	f.term.Write(apc)

	stamped := f.term.KittyGeneration()
	f.generation = stamped
	f.noteTransmission(apc, stamped != generation)
	movedCol, movedRow := f.term.CursorPos()
	if stamped == generation && movedCol == col && movedRow == row {
		freeTrackedRef(before)
		return
	}

	if stamped != generation {
		f.observe()
	}

	after := f.term.TrackCursor()
	anchor, landed, ok := trackedRows(before, after)
	freeTrackedRef(before)
	freeTrackedRef(after)
	if !ok {
		f.failResync(kittyResyncAnchorLost)
		return
	}

	// The tracked pair reports cursor movement relative to CONTENT; taking the viewport
	// movement back out leaves the scroll. Holds on both screens and inside a scroll region.
	scrolled := (row - movedRow) + (landed - anchor)
	if scrolled < 0 {
		f.failResync(kittyResyncReverseScroll)
		return
	}
	// On the alternate screen an anchor at row 0 only means the pin was CLAMPED there, and
	// the scroll amount is unrecoverable: fitting and scrolling the top row away both read 0.
	if anchor == 0 && f.term.AltScreenActive() {
		f.failResync(kittyResyncAnchorClamped)
		return
	}

	// One SU carries at most a screen's worth of rows (ghostty clamps to the
	// scroll region), so a taller scroll would leave the client's history short.
	if _, screenRows := f.term.Size(); scrolled > screenRows {
		f.failResync(kittyResyncScrollClamped)
		return
	}

	if f.term.LeftRightMarginMode() {
		f.failResync(kittyResyncMarginMode)
	}

	if screenCols, _ := f.term.Size(); col == screenCols-1 {
		f.failResync(kittyResyncPendingWrap)
	}

	f.wire = appendCSI(f.wire, scrolled, 'S')
	if movedRow > row {
		f.wire = appendCSI(f.wire, movedRow-row, 'B')
	} else {
		f.wire = appendCSI(f.wire, row-movedRow, 'A')
	}
	if movedCol > col {
		f.wire = appendCSI(f.wire, movedCol-col, 'C')
	} else {
		f.wire = appendCSI(f.wire, col-movedCol, 'D')
	}
}

type kittyTransmission struct {
	ask     uint64
	payload uint64
}

// stored says whether ghostty's kitty generation moved on this escape. Measured: it stays
// put on an over-limit transmission and every intermediate m=1 escape; eviction is invisible.
func (f *wireFeeder) noteTransmission(apc []byte, stored bool) {
	ask, more, ok := parseKittyTransmission(apc)
	if !ok {
		return
	}
	f.pending.payload += ask.payload
	if ask.ask > 0 {
		f.pending.ask = ask.ask
	}
	if more {
		return
	}

	want := f.pending.ask
	if want == 0 {
		want = f.pending.payload
	}
	f.pending = kittyTransmission{}
	if stored || f.logf == nil {
		return
	}
	f.logf(
		"pty kitty storage: an image transmission stored nothing — %s=%d bytes, this image asks for about %d. "+
			"An image larger than the whole limit is refused outright; raise the limit or have the program send a smaller one. "+
			"(Evicting an older image to fit a new one is not this, and is never logged.)",
		kittyStorageLimitEnv,
		f.kittyLimit,
		want,
	)
}

// parseKittyTransmission reads the keys a refusal check needs out of one complete APC. It
// treats what it does not recognize as absent; kitty's default action `t` is assumed.
func parseKittyTransmission(apc []byte) (t kittyTransmission, more bool, ok bool) {
	body := apc
	body = bytes.TrimPrefix(body, []byte("\x1b_G"))
	if len(body) == len(apc) {
		return t, false, false
	}
	body = bytes.TrimSuffix(bytes.TrimSuffix(body, []byte("\x1b\\")), []byte{0x9c})

	control := body
	if i := bytes.IndexByte(body, ';'); i >= 0 {
		control = body[:i]
		t.payload = uint64(len(body)-i-1) * 3 / 4
	}

	action := byte('t')
	var width, height uint64
	for _, pair := range bytes.Split(control, []byte(",")) {
		key, value, found := bytes.Cut(pair, []byte("="))
		if !found || len(key) != 1 || len(value) == 0 {
			continue
		}
		switch key[0] {
		case 'a':
			action = value[0]
		case 'm':
			more = value[0] == '1'
		case 's':
			width, _ = strconv.ParseUint(string(value), 10, 64)
		case 'v':
			height, _ = strconv.ParseUint(string(value), 10, 64)
		}
	}
	if action != 't' && action != 'T' {
		return kittyTransmission{}, false, false
	}
	// Ghostty stores decoded RGBA whatever the wire format was.
	t.ask = width * height * 4
	return t, more, true
}

func appendCSI(dst []byte, n int, final byte) []byte {
	if n == 0 {
		return dst
	}
	dst = append(dst, 0x1b, '[')
	dst = strconv.AppendInt(dst, int64(n), 10)
	return append(dst, final)
}

var placementReadHook func()

func (f *wireFeeder) readPlacements() []ghosttyvt.KittyPlacement {
	if placementReadHook != nil {
		placementReadHook()
	}
	placements := f.term.KittyPlacements()
	for i := range placements {
		placements[i].ImageGeneration += f.epoch
	}
	return placements
}

func (f *wireFeeder) observe() {
	current := f.readPlacements()
	delta := diffKittyPlacements(f.placements, current)
	f.placements = current
	if !delta.empty() {
		f.deltas = append(f.deltas, delta)
	}
}

// settleUnaccounted closes the books on kitty state changes no writeAPC accounted for. It
// must run at the entry of every writeAPC, or a described APC absorbs an undescribed move.
func (f *wireFeeder) settleUnaccounted() bool {
	stamped := f.term.KittyGeneration()
	if stamped == f.generation {
		return false
	}
	f.generation = stamped

	before := len(f.deltas)
	f.observe()
	if reason, ok := unaccountedResync(f.deltas[before:]); ok {
		f.failResync(reason)
	}
	return true
}

func unaccountedResync(deltas []kittyPlacementDelta) (string, bool) {
	if len(deltas) == 0 {
		return kittyResyncStampWithoutDelta, true
	}
	for _, delta := range deltas {
		if len(delta.Added) > 0 || len(delta.Updated) > 0 {
			return kittyResyncUndescribedImage, true
		}
	}
	return "", false
}

func (f *wireFeeder) failResync(reason string) {
	if f.resync == "" {
		f.resync = reason
	}
}

// changedPlacements reports the active screen's whole placement set when this feed moved
// it. No copy needed: observe REPLACES the set rather than mutating it.
func (f *wireFeeder) changedPlacements() ([]ghosttyvt.KittyPlacement, bool) {
	if len(f.deltas) == 0 {
		return nil, false
	}
	return f.placements, true
}

func (f *wireFeeder) snapshotBlocks() []AttachBlockData {
	return f.blocks.snapshotBlocks()
}

// snapshotPlacements reads fresh from the terminal, not from the last observation: a resize
// reflows under the same lock. The bool says whether this feeder holds ANY placement.
func (f *wireFeeder) snapshotPlacements() ([]ghosttyvt.KittyPlacement, bool) {
	if len(f.placements) == 0 {
		return nil, false
	}
	return f.readPlacements(), true
}

// restoreBlocks seeds the block table from a handoff snapshot. Caller holds
// replayMu; the VT dump must already be replayed, or there are no rows to pin.
func (f *wireFeeder) restoreBlocks(blocks []AttachBlockData) {
	f.blocks.restore(blocks)
}

// close frees the native refs the block table holds; must run before the
// terminal itself is closed.
func (f *wireFeeder) close() {
	f.blocks.close()
}

// trackedRows resolves where the cursor's cell ended up and where it is now, in rows from
// the top of history. Both read AFTER the write so they share one coordinate frame.
func trackedRows(before, after *ghosttyvt.TrackedRef) (anchor, landed int, ok bool) {
	if before == nil || after == nil {
		return 0, 0, false
	}
	_, anchor, ok = before.ScreenPoint()
	if !ok {
		return 0, 0, false
	}
	_, landed, ok = after.ScreenPoint()
	if !ok {
		return 0, 0, false
	}
	return anchor, landed, true
}

func freeTrackedRef(ref *ghosttyvt.TrackedRef) {
	if ref != nil {
		ref.Free()
	}
}

func diffKittyPlacements(before, after []ghosttyvt.KittyPlacement) kittyPlacementDelta {
	var delta kittyPlacementDelta
	if len(before) == 0 && len(after) == 0 {
		return delta
	}

	prior := make(map[kittyPlacementKey]ghosttyvt.KittyPlacement, len(before))
	for _, p := range before {
		prior[kittyPlacementKey{ImageID: p.ImageID, PlacementID: p.PlacementID}] = p
	}
	live := make(map[kittyPlacementKey]struct{}, len(after))
	for _, p := range after {
		key := kittyPlacementKey{ImageID: p.ImageID, PlacementID: p.PlacementID}
		live[key] = struct{}{}
		switch old, ok := prior[key]; {
		case !ok:
			delta.Added = append(delta.Added, p)
		case old != p:
			delta.Updated = append(delta.Updated, p)
		}
	}
	for _, p := range before {
		key := kittyPlacementKey{ImageID: p.ImageID, PlacementID: p.PlacementID}
		if _, ok := live[key]; !ok {
			delta.Removed = append(delta.Removed, key)
		}
	}
	return delta
}
