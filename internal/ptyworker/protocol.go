package ptyworker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/pty"
)

const (
	RPCMajor = 1
	RPCMinor = 1
)

const MinCompatibleRPCMinor = 0

// snapshot, set_theme, kitty_image and upgrade were added without an RPC version
// bump: an older worker rejects them with ErrBadRequest ("unknown method").
const (
	MethodHello          = "hello"
	MethodInfo           = "info"
	MethodScreenSnapshot = "snapshot"
	MethodAttach         = "attach"
	MethodWatch          = "watch"
	MethodDetach         = "detach"
	MethodInput          = "input"
	MethodResize         = "resize"
	MethodSetTheme       = "set_theme"
	MethodSignal         = "signal"
	MethodRemove         = "remove"
	MethodHealth         = "health"
	MethodKittyImage     = "kitty_image"
	MethodUpgrade        = "upgrade"
)

const (
	EventOutput            = "output"
	EventDesync            = "desync"
	EventStateChanged      = "state_changed"
	EventExit              = "exit"
	EventTeardownEscalated = "teardown_escalated"
	// Carries the FULL placement set as of the chunk stamped Seq, the empty set
	// included: that is how a client learns the last image is gone.
	EventKittyPlacements = "kitty_placements"
)

const (
	ErrBadRequest         = "bad_request"
	ErrUnsupportedVersion = "unsupported_version"
	ErrUnauthorized       = "unauthorized"
	ErrSessionNotFound    = "session_not_found"
	ErrSessionNotRunning  = "session_not_running"
	ErrIO                 = "io_error"
	ErrInternal           = "internal_error"
	ErrImageNotFound      = "image_not_found"
)

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RequestEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ResponseEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type EventEnvelope struct {
	Type       string  `json:"type"`
	Event      string  `json:"event"`
	SessionID  string  `json:"session_id"`
	Seq        *uint32 `json:"seq,omitempty"`
	Data       *string `json:"data,omitempty"`
	Reason     *string `json:"reason,omitempty"`
	State      *string `json:"state,omitempty"`
	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`

	StateSource     *string `json:"state_source,omitempty"`
	StateDetail     *string `json:"state_detail,omitempty"`
	StateObservedAt *string `json:"state_observed_at,omitempty"`

	// An absent array on EventKittyPlacements is the empty set.
	Placements []KittyPlacement `json:"placements,omitempty"`
}

// Viewport row and column are screen-relative on the worker's grid; a client
// maps them by adding its own scrollback length.
type KittyPlacement struct {
	ImageID         uint32 `json:"image_id"`
	PlacementID     uint32 `json:"placement_id"`
	Virtual         bool   `json:"virtual,omitempty"`
	Z               int32  `json:"z,omitempty"`
	PixelWidth      uint32 `json:"pixel_width"`
	PixelHeight     uint32 `json:"pixel_height"`
	GridCols        uint32 `json:"grid_cols"`
	GridRows        uint32 `json:"grid_rows"`
	ViewportCol     int32  `json:"viewport_col"`
	ViewportRow     int32  `json:"viewport_row"`
	ViewportVisible bool   `json:"viewport_visible,omitempty"`
	SourceX         uint32 `json:"source_x,omitempty"`
	SourceY         uint32 `json:"source_y,omitempty"`
	SourceWidth     uint32 `json:"source_width,omitempty"`
	SourceHeight    uint32 `json:"source_height,omitempty"`
	ImageGeneration uint64 `json:"image_generation"`
}

func placementsToWire(placements []pty.KittyPlacement) []KittyPlacement {
	if len(placements) == 0 {
		return nil
	}
	out := make([]KittyPlacement, len(placements))
	for i, p := range placements {
		out[i] = KittyPlacement{
			ImageID:         p.ImageID,
			PlacementID:     p.PlacementID,
			Virtual:         p.Virtual,
			Z:               p.Z,
			PixelWidth:      p.PixelWidth,
			PixelHeight:     p.PixelHeight,
			GridCols:        p.GridCols,
			GridRows:        p.GridRows,
			ViewportCol:     p.ViewportCol,
			ViewportRow:     p.ViewportRow,
			ViewportVisible: p.ViewportVisible,
			SourceX:         p.SourceX,
			SourceY:         p.SourceY,
			SourceWidth:     p.SourceWidth,
			SourceHeight:    p.SourceHeight,
			ImageGeneration: p.ImageGeneration,
		}
	}
	return out
}

func PlacementsFromWire(placements []KittyPlacement) []pty.KittyPlacement {
	if len(placements) == 0 {
		return nil
	}
	out := make([]pty.KittyPlacement, len(placements))
	for i, p := range placements {
		out[i] = pty.KittyPlacement{
			ImageID:         p.ImageID,
			PlacementID:     p.PlacementID,
			Virtual:         p.Virtual,
			Z:               p.Z,
			PixelWidth:      p.PixelWidth,
			PixelHeight:     p.PixelHeight,
			GridCols:        p.GridCols,
			GridRows:        p.GridRows,
			ViewportCol:     p.ViewportCol,
			ViewportRow:     p.ViewportRow,
			ViewportVisible: p.ViewportVisible,
			SourceX:         p.SourceX,
			SourceY:         p.SourceY,
			SourceWidth:     p.SourceWidth,
			SourceHeight:    p.SourceHeight,
			ImageGeneration: p.ImageGeneration,
		}
	}
	return out
}

type HelloParams struct {
	RPCMajor         int    `json:"rpc_major"`
	RPCMinor         int    `json:"rpc_minor"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	ControlToken     string `json:"control_token"`
}

type HelloResult struct {
	WorkerVersion    string `json:"worker_version"`
	RPCMajor         int    `json:"rpc_major"`
	RPCMinor         int    `json:"rpc_minor"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	SessionID        string `json:"session_id"`
	// An absent format reads as a mismatch: those are exactly the workers a
	// libghostty-vt bump strands.
	SnapshotFormat string `json:"snapshot_format,omitempty"`
}

type InfoResult struct {
	Running   bool   `json:"running"`
	Agent     string `json:"agent"`
	CWD       string `json:"cwd"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
	WorkerPID int    `json:"worker_pid"`
	ChildPID  int    `json:"child_pid"`
	LastSeq   uint32 `json:"last_seq"`
	State     string `json:"state"`

	LastSignalClaim  string `json:"last_signal_claim,omitempty"`
	LastSignalDetail string `json:"last_signal_detail,omitempty"`
	LastSignalSource string `json:"last_signal_source,omitempty"`
	LastSignalAt     string `json:"last_signal_at,omitempty"`

	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`
}

type AttachResult struct {
	LastSeq uint32 `json:"last_seq"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	PID     int    `json:"pid"`
	Running bool   `json:"running"`

	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`

	GhosttySnapshot       []byte `json:"ghostty_snapshot,omitempty"`
	GhosttySnapshotFormat string `json:"ghostty_snapshot_format,omitempty"`
	// Rows are SCREEN-space in GhosttySnapshot, captured atomically with it.
	GhosttyBlocks              []AttachBlock    `json:"ghostty_blocks,omitempty"`
	GhosttyPlacements          []KittyPlacement `json:"ghostty_placements,omitempty"`
	GhosttyScrollbackTruncated bool             `json:"ghostty_scrollback_truncated,omitempty"`
}

// A worker never resolves its own replacement: after an install os.Executable()
// can point at a path that was replaced underneath it.
type UpgradeParams struct {
	Executable string `json:"executable"`
}

// Sent BEFORE the exec, because the exec ends the connection.
type UpgradeResult struct {
	ChildPID   int `json:"child_pid"`
	DumpBytes  int `json:"dump_bytes"`
	BlockCount int `json:"block_count"`
}

type ScreenSnapshotResult struct {
	LastSeq        uint32 `json:"last_seq"`
	Cols           uint16 `json:"cols"`
	Rows           uint16 `json:"rows"`
	Running        bool   `json:"running"`
	ScreenSnapshot []byte `json:"screen_snapshot,omitempty"`
	// A pointer so an old worker's omission differs from a blank viewport.
	ScreenText *string `json:"screen_text,omitempty"`
	ScreenCols uint16  `json:"screen_cols,omitempty"`
	ScreenRows uint16  `json:"screen_rows,omitempty"`
}

// EndRow is exclusive; Pending marks the single open block.
type AttachBlock struct {
	ID             uint64  `json:"id"`
	Pending        bool    `json:"pending,omitempty"`
	PromptRow      int32   `json:"prompt_row"`
	InputRow       *int32  `json:"input_row,omitempty"`
	InputCol       *int32  `json:"input_col,omitempty"`
	OutputStartRow *int32  `json:"output_start_row,omitempty"`
	EndRow         *int32  `json:"end_row,omitempty"`
	Command        *string `json:"command,omitempty"`
	ExitCode       *int32  `json:"exit_code,omitempty"`
}

type AttachParams struct {
	SubscriberID string `json:"subscriber_id,omitempty"`
	OmitReplay   bool   `json:"omit_replay,omitempty"`
}

type InputParams struct {
	Data string `json:"data"`
}

// XPixel/YPixel are the pane's total size in device pixels, 0 when unreported.
type ResizeParams struct {
	Cols   uint16 `json:"cols"`
	Rows   uint16 `json:"rows"`
	XPixel uint16 `json:"xpixel,omitempty"`
	YPixel uint16 `json:"ypixel,omitempty"`
}

type ResizeResult struct {
	OK bool `json:"ok"`
	// Pointer distinguishes an old {ok:true} from a new authoritative no-op.
	Changed *bool `json:"changed,omitempty"`
}

type SignalParams struct {
	Signal string `json:"signal"`
}

type SetThemeParams struct {
	Foreground  string     `json:"foreground"`
	Background  string     `json:"background"`
	Cursor      string     `json:"cursor"`
	ANSIPalette [16]string `json:"ansi_palette"`
}

type KittyImageParams struct {
	ImageID uint32 `json:"image_id"`
}

// Data is base64'd RAW PIXELS (Width*Height*bpp). Generation pairs with ImageID:
// a retransmitted id replaces the pixels, so an id-only cache serves stale ones.
type KittyImageResult struct {
	ImageID    uint32 `json:"image_id"`
	Width      uint32 `json:"width"`
	Height     uint32 `json:"height"`
	Format     string `json:"format"`
	Generation uint64 `json:"generation"`
	Data       string `json:"data"`
}

// Spelled out rather than passed through as ghostty's enum ordinal: this RPC
// crosses a version boundary, and a pin bump can renumber the layouts.
const (
	kittyFormatRGB       = "rgb"
	kittyFormatRGBA      = "rgba"
	kittyFormatGrayAlpha = "gray_alpha"
	kittyFormatGray      = "gray"
)

func kittyImageToWire(img pty.KittyImage) (KittyImageResult, error) {
	var format string
	switch img.Format {
	case ghosttyvt.KittyImageRGB:
		format = kittyFormatRGB
	case ghosttyvt.KittyImageRGBA:
		format = kittyFormatRGBA
	case ghosttyvt.KittyImageGrayAlpha:
		format = kittyFormatGrayAlpha
	case ghosttyvt.KittyImageGray:
		format = kittyFormatGray
	default:
		return KittyImageResult{}, fmt.Errorf("kitty image %d has unknown pixel format %d", img.ID, img.Format)
	}
	return KittyImageResult{
		ImageID:    img.ID,
		Width:      img.Width,
		Height:     img.Height,
		Format:     format,
		Generation: img.Generation,
		Data:       base64.StdEncoding.EncodeToString(img.Data),
	}, nil
}

func (r KittyImageResult) Decode() (pty.KittyImage, error) {
	var format ghosttyvt.KittyImageFormat
	switch r.Format {
	case kittyFormatRGB:
		format = ghosttyvt.KittyImageRGB
	case kittyFormatRGBA:
		format = ghosttyvt.KittyImageRGBA
	case kittyFormatGrayAlpha:
		format = ghosttyvt.KittyImageGrayAlpha
	case kittyFormatGray:
		format = ghosttyvt.KittyImageGray
	default:
		return pty.KittyImage{}, fmt.Errorf("kitty image %d has unknown pixel format %q", r.ImageID, r.Format)
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		return pty.KittyImage{}, fmt.Errorf("decode kitty image %d pixels: %w", r.ImageID, err)
	}
	return pty.KittyImage{
		ID:         r.ImageID,
		Width:      r.Width,
		Height:     r.Height,
		Format:     format,
		Generation: r.Generation,
		Data:       data,
	}, nil
}

func IsCompatibleVersion(peerMajor, peerMinor int) bool {
	if peerMajor != RPCMajor {
		return false
	}
	if peerMinor < MinCompatibleRPCMinor {
		return false
	}
	if peerMinor > RPCMinor {
		return false
	}
	return true
}

func stateChangedEvent(sessionID string, obs pty.Observation) EventEnvelope {
	claim := obs.Claim
	source := string(obs.Source)
	evt := EventEnvelope{
		Type:        "evt",
		Event:       EventStateChanged,
		SessionID:   sessionID,
		State:       &claim,
		StateSource: &source,
	}
	if obs.Detail != "" {
		detail := obs.Detail
		evt.StateDetail = &detail
	}
	if !obs.At.IsZero() {
		at := obs.At.Format(time.RFC3339Nano)
		evt.StateObservedAt = &at
	}
	return evt
}

func ObservationFromEvent(evt EventEnvelope, claim string, fallbackAt time.Time) pty.Observation {
	obs := pty.Observation{
		Source: pty.SourceUnknown,
		Claim:  claim,
		At:     fallbackAt,
	}
	if evt.StateSource != nil && *evt.StateSource != "" {
		obs.Source = pty.Source(*evt.StateSource)
	}
	if evt.StateDetail != nil {
		obs.Detail = *evt.StateDetail
	}
	if evt.StateObservedAt != nil {
		if at, err := time.Parse(time.RFC3339Nano, *evt.StateObservedAt); err == nil && !at.IsZero() {
			obs.At = at
		}
	}
	return obs
}
