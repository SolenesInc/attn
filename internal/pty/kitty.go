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

// Deliberately an alias, not a mirror struct: a hand-maintained copy would rot
// on a pin bump.
type KittyPlacement = ghosttyvt.KittyPlacement

// Always raw pixels in Format's layout, never PNG — ghostty decodes before storing.
type KittyImage = ghosttyvt.KittyImage

// The FULL placement set of the active screen as of the chunk stamped Seq — never a delta,
// and the empty set is an ordinary update. Delivered after the chunk with the same seq.
type PlacementUpdate struct {
	Seq        uint32
	Placements []KittyPlacement
}

var ErrKittyImageNotFound = errors.New("kitty image not found")

type SubscriberOption func(*sessionSubscriber)

// Rides the subscriber, not a session-wide handler: an update is only meaningful
// in order against that subscriber's own byte stream.
func OnPlacements(fn func(PlacementUpdate)) SubscriberOption {
	return func(sub *sessionSubscriber) {
		sub.onPlacements = fn
	}
}

// In BYTES. Zero is special: ghostty then refuses every transmission, so nothing
// is stored, no placement is observed, and the feed never leaves its no-cgo path.
const kittyStorageLimitEnv = "ATTN_KITTY_STORAGE_LIMIT"

// 320MB — ghostty's own app default. Sits ~4x past the largest measured single image, ~81.4MB
// (2x XDR full-screen capture); an image past the whole limit is refused silently.
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

// Window [2^32, 2^52): generations ride JSON into JS Numbers, exact only to 2^53, and 2^32 is
// past any stamp a real process reaches. Both folds must use the same value.
func mintKittyEpoch() uint64 {
	const floor = uint64(1) << 32
	const span = uint64(1)<<52 - floor
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return floor + uint64(time.Now().UnixNano())%span
	}
	return floor + binary.BigEndian.Uint64(b[:])%span
}
