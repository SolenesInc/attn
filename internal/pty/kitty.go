package pty

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

type KittyPlacement = ghosttyvt.KittyPlacement

type KittyImage = ghosttyvt.KittyImage

type PlacementUpdate struct {
	Seq        uint32
	Placements []KittyPlacement
}

type ResizeUpdate struct {
	Cols   uint16
	Rows   uint16
	XPixel uint16
	YPixel uint16
}

var ErrKittyImageNotFound = errors.New("kitty image not found")

type SubscriberOption func(*sessionSubscriber)

func OnPlacements(fn func(PlacementUpdate)) SubscriberOption {
	return func(sub *sessionSubscriber) {
		sub.onPlacements = fn
	}
}

func OnResize(fn func(ResizeUpdate)) SubscriberOption {
	return func(sub *sessionSubscriber) {
		sub.onResize = fn
	}
}

const kittyStorageLimitEnv = "ATTN_KITTY_STORAGE_LIMIT"

const kittyStorageLimitDefault = 320_000_000

func kittyStorageLimit(logf LogFunc) uint64 {
	raw := strings.TrimSpace(os.Getenv(kittyStorageLimitEnv))
	if raw == "" {
		return kittyStorageLimitDefault
	}
	limit, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		if logf != nil {
			logf(
				"pty kitty storage: ignoring %s=%q, want a byte count; images run at the default %d bytes for this session",
				kittyStorageLimitEnv,
				raw,
				uint64(kittyStorageLimitDefault),
			)
		}
		return kittyStorageLimitDefault
	}
	return limit
}

func mintKittyEpoch() uint64 {
	const floor = uint64(1) << 32
	const span = uint64(1)<<52 - floor
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return floor + uint64(time.Now().UnixNano())%span
	}
	return floor + binary.BigEndian.Uint64(b[:])%span
}
