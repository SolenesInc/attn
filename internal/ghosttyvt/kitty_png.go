//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

/*
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <ghostty/vt.h>
*/
import "C"

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"unsafe"
)

// Ghostty validates dimensions only after decoding and this hook is the decoder, so without
// these a 20000x20000 IHDR sizes a 1.6GB allocation (limits from graphics_image.zig).
const (
	maxKittyImageDimension = 10000
	maxKittyImageBytes     = 400 * 1024 * 1024
)

// The output buffer MUST come from the allocator ghostty passes in: it takes ownership and
// frees with that allocator. The bytes are attacker-controlled, so a decode panic is recovered.
//
//export goDecodePNG
func goDecodePNG(userdata unsafe.Pointer, allocator *C.GhosttyAllocator, data *C.uint8_t, dataLen C.size_t, out *C.GhosttySysImage) (ok C._Bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	if data == nil || dataLen == 0 || out == nil {
		return false
	}

	raw := C.GoBytes(unsafe.Pointer(data), C.int(dataLen))

	// png.Decode allocates the whole pixel buffer from the header dimensions, so
	// the size the sender claims has to be judged before that call, not after.
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return false
	}
	if cfg.Width > maxKittyImageDimension || cfg.Height > maxKittyImageDimension {
		return false
	}
	if int64(cfg.Width)*int64(cfg.Height)*4 > maxKittyImageBytes {
		return false
	}

	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return false
	}
	// NRGBA is straight alpha, which is what the kitty protocol's RGBA means; image.RGBA would
	// hand ghostty premultiplied pixels and darken every translucent image.
	rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	buf := C.ghostty_alloc(allocator, C.size_t(len(rgba.Pix)))
	if buf == nil {
		return false
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(buf)), len(rgba.Pix)), rgba.Pix)
	out.width = C.uint32_t(b.Dx())
	out.height = C.uint32_t(b.Dy())
	out.data = buf
	out.data_len = C.size_t(len(rgba.Pix))
	return true
}
