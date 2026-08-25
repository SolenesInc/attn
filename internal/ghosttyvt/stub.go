//go:build !cgo || !((darwin && arm64) || (linux && amd64) || (linux && arm64))

package ghosttyvt

const DefaultScrollbackBytes = 8 << 20

const ContinuationMaxBytes = 65 << 20

type Options struct {
	ScrollbackBytes int

	// The zero value disables the kitty protocol entirely (the library's own default is 10MB).
	KittyImageStorageLimit uint64
}

type Snapshot struct {
	Cols, Rows int
	Payload    []byte
	VTDump     []byte
}

type Terminal struct {
	cols, rows int
}

func New(cols, rows int, _ Options) (*Terminal, error) {
	return &Terminal{cols: cols, rows: rows}, nil
}

func (t *Terminal) Write(_ []byte) {}

func (t *Terminal) SetColorTheme(_ ColorTheme) error { return nil }

func (t *Terminal) Resize(cols, rows int) {
	if cols > 0 && rows > 0 {
		t.cols, t.rows = cols, rows
	}
}

func (t *Terminal) ResizeNoReflow(cols, rows int) { t.Resize(cols, rows) }

func (t *Terminal) SetCellPixelSize(_, _ int) {}

func (t *Terminal) DrainResponses() []byte { return nil }

func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

func (t *Terminal) PlainText() string { return "" }

func (t *Terminal) Serialize() Snapshot { return Snapshot{Cols: t.cols, Rows: t.rows} }

func (t *Terminal) CursorPos() (x, y int) { return 0, 0 }

func (t *Terminal) CursorVisible() bool { return false }

func (t *Terminal) LeftRightMarginMode() bool { return false }

func (t *Terminal) ViewportText() string { return "" }

func (t *Terminal) SerializeViewport() Snapshot { return Snapshot{Cols: t.cols, Rows: t.rows} }
func (t *Terminal) HandoffVT() Snapshot         { return Snapshot{Cols: t.cols, Rows: t.rows} }
func (t *Terminal) TotalRows() int              { return t.rows }

func (t *Terminal) Close() {}

type TrackedRef struct{}

func (r *TrackedRef) ScreenPoint() (x, y int, ok bool) { return 0, 0, false }

func (r *TrackedRef) Free() {}

func (t *Terminal) TrackCursor() *TrackedRef { return nil }

func (t *Terminal) TrackPoint(x, y int) *TrackedRef { return nil }

func (t *Terminal) AltScreenActive() bool { return false }

func LiveTrackedRefs() int { return 0 }

type KittyImageFormat uint8

const (
	KittyImageRGB KittyImageFormat = iota
	KittyImageRGBA
	KittyImageGrayAlpha
	KittyImageGray
)

type KittyPlacement struct {
	ImageID         uint32
	PlacementID     uint32
	Virtual         bool
	Z               int32
	PixelWidth      uint32
	PixelHeight     uint32
	GridCols        uint32
	GridRows        uint32
	ViewportCol     int32
	ViewportRow     int32
	ViewportVisible bool
	SourceX         uint32
	SourceY         uint32
	SourceWidth     uint32
	SourceHeight    uint32
	ImageGeneration uint64
}

type KittyImage struct {
	ID         uint32
	Width      uint32
	Height     uint32
	Format     KittyImageFormat
	Generation uint64
	Data       []byte
}

func (t *Terminal) KittyGeneration() uint64 { return 0 }

func (t *Terminal) KittyPlacements() []KittyPlacement { return nil }

func (t *Terminal) KittyImage(_ uint32) (KittyImage, bool) { return KittyImage{}, false }
