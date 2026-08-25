package ptybackend

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyworker"
)

// Cells are 8x16 px, so a 16x32 image is exactly two cells by two.
func kittyPlaceRGB(id uint32, w, h int) string {
	pix := make([]byte, w*h*3)
	for i := range pix {
		pix[i] = byte((i*7 + 13) % 251)
	}
	return fmt.Sprintf("\x1b_Ga=T,i=%d,f=24,t=d,s=%d,v=%d;%s\x1b\\",
		id, w, h, base64.StdEncoding.EncodeToString(pix))
}

func kittyPayloadFile(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return path
}

// The child blocks on a newline: without that handshake it can emit and finish
// before the attach lands.
func releaseAndReadPlacements(t *testing.T, stream Stream, release func() error) OutputEvent {
	t.Helper()
	if err := release(); err != nil {
		t.Fatalf("release the child: %v", err)
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case evt, ok := <-stream.Events():
			if !ok {
				t.Fatal("the stream closed before any placement arrived")
			}
			if evt.Kind == OutputEventKindPlacements {
				return evt
			}
		case <-deadline:
			t.Fatal("timed out waiting for a placement event")
		}
	}
}

func TestConvertWorkerEventCarriesPlacements(t *testing.T) {
	seq := uint32(77)
	evt, ok := convertWorkerEvent(ptyworker.EventEnvelope{
		Type:      "evt",
		Event:     ptyworker.EventKittyPlacements,
		SessionID: "s1",
		Seq:       &seq,
		Placements: []ptyworker.KittyPlacement{{
			ImageID:     4,
			PlacementID: 1,
			GridCols:    2,
			GridRows:    2,
			ViewportRow: 5,
			PixelWidth:  16,
			PixelHeight: 32,
		}},
	})
	if !ok {
		t.Fatal("a kitty placements event was dropped at the backend boundary")
	}
	if evt.Kind != OutputEventKindPlacements {
		t.Fatalf("kind = %q, want %q", evt.Kind, OutputEventKindPlacements)
	}
	if evt.Seq != seq {
		t.Errorf("seq = %d, want %d: the set only means anything against its own chunk", evt.Seq, seq)
	}
	if len(evt.Placements) != 1 || evt.Placements[0].ImageID != 4 || evt.Placements[0].ViewportRow != 5 {
		t.Errorf("placements = %+v, want the described image at row 5", evt.Placements)
	}
}

func TestConvertWorkerEventCarriesTheEmptyPlacementSet(t *testing.T) {
	seq := uint32(78)
	evt, ok := convertWorkerEvent(ptyworker.EventEnvelope{
		Type:      "evt",
		Event:     ptyworker.EventKittyPlacements,
		SessionID: "s1",
		Seq:       &seq,
	})
	if !ok {
		t.Fatal("an empty placement set was dropped; nothing else clears a removed image")
	}
	if evt.Kind != OutputEventKindPlacements || len(evt.Placements) != 0 {
		t.Errorf("event = %+v, want an empty placement set", evt)
	}
}

func TestWorkerBackend_KittyPlacementsAndImagePull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping worker integration test in short mode")
	}
	if os.Getenv("ATTN_RUN_WORKER_INTEGRATION") != "1" {
		t.Skip("set ATTN_RUN_WORKER_INTEGRATION=1 to run worker integration test")
	}
	t.Setenv("ATTN_KITTY_STORAGE_LIMIT", "16777216")

	binary := buildAttnBinary(t)
	root, err := os.MkdirTemp("/tmp", "attn-worker-kitty-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	defer os.RemoveAll(root)

	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BinaryPath:       binary,
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "worker-kitty-1"
	payload := kittyPayloadFile(t, "\x1b[3;1H"+kittyPlaceRGB(90, 16, 32))
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:              sessionID,
		CWD:             t.TempDir(),
		Agent:           "probe-kitty",
		ExternalCommand: []string{"/bin/sh", "-c", "read release; cat " + payload},
		Cols:            40,
		Rows:            12,
	}); err != nil {
		t.Skipf("worker spawn unavailable in this environment: %v", err)
	}
	defer func() { _ = backend.Remove(context.Background(), sessionID) }()

	_, stream, err := backend.Attach(context.Background(), sessionID, "kitty-sub")
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	defer stream.Close()

	evt := releaseAndReadPlacements(t, stream, func() error {
		return backend.Input(context.Background(), sessionID, []byte("\n"))
	})
	if len(evt.Placements) != 1 || evt.Placements[0].ImageID != 90 {
		t.Fatalf("placements = %+v, want the image the program emitted", evt.Placements)
	}

	img, err := backend.KittyImage(context.Background(), sessionID, evt.Placements[0].ImageID)
	if err != nil {
		t.Fatalf("KittyImage() error: %v", err)
	}
	if img.Width != 16 || img.Height != 32 || len(img.Data) != 16*32*3 {
		t.Errorf("fetched image = %dx%d with %d bytes, want 16x32 with %d", img.Width, img.Height, len(img.Data), 16*32*3)
	}

	if _, err := backend.KittyImage(context.Background(), sessionID, 999); !errors.Is(err, pty.ErrKittyImageNotFound) {
		t.Errorf("KittyImage() for an id the worker never stored = %v, want ErrKittyImageNotFound", err)
	}
}
