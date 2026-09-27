//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

const ghosttyMaxDimension = 10000

func TestKittyPNGTransmissionsAreDecodedWithinGhosttysLimits(t *testing.T) {
	term, err := New(20, 8, Options{KittyImageStorageLimit: 10 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(term.Close)
	if pngDecoderRC != 0 {
		t.Fatalf("PNG decode hook install returned rc=%d", int(pngDecoderRC))
	}
	alpha := []color.NRGBA{
		{R: 255, G: 0, B: 0, A: 255}, {R: 0, G: 255, B: 0, A: 255}, {R: 0, G: 0, B: 255, A: 255},
		{R: 255, G: 0, B: 0, A: 128}, {R: 10, G: 20, B: 30, A: 40}, {R: 255, G: 255, B: 255, A: 0},
	}
	alphaImage := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	var alphaBytes []byte
	for i, c := range alpha {
		alphaImage.SetNRGBA(i%3, i/3, c)
		alphaBytes = append(alphaBytes, c.R, c.G, c.B, c.A)
	}
	var alphaPNG bytes.Buffer
	if err := png.Encode(&alphaPNG, alphaImage); err != nil {
		t.Fatal(err)
	}

	for i, tc := range []struct {
		name          string
		png           []byte
		stored        bool
		width, height uint32
		pixels        []byte
		response      string
	}{
		{name: "straight alpha RGBA", png: alphaPNG.Bytes(), stored: true, width: 3, height: 2, pixels: alphaBytes},
		{name: "one pixel past the dimension cap", png: encodeWidePNG(t, ghosttyMaxDimension+1), response: "EINVAL: invalid data"},
		{name: "at the dimension cap", png: encodeWidePNG(t, ghosttyMaxDimension), stored: true, width: ghosttyMaxDimension, height: 1},
		{name: "an IHDR claiming 20000x20000", png: craftedPNG(t, 20000, 20000)},
		{name: "a valid image after the hostile one", png: encodeWidePNG(t, 4), stored: true, width: 4, height: 1},
	} {
		id := uint32(80 + i)
		term.DrainResponses()
		term.Write([]byte(kittyTransmitPNG(id, tc.png)))
		response := string(term.DrainResponses())
		img, ok := term.KittyImage(id)
		switch {
		case ok != tc.stored:
			t.Errorf("%s: stored = %v, want %v (response %q)", tc.name, ok, tc.stored, response)
		case ok && (img.Format != KittyImageRGBA || img.Width != tc.width || img.Height != tc.height):
			t.Errorf("%s: stored format %d at %dx%d, want RGBA at %dx%d", tc.name, img.Format, img.Width, img.Height, tc.width, tc.height)
		case ok && tc.pixels != nil && !bytes.Equal(img.Data, tc.pixels):
			t.Errorf("%s: pixels %v, want %v", tc.name, img.Data, tc.pixels)
		case !strings.Contains(response, tc.response) || strings.Contains(response, "dimensions too large"):
			t.Errorf("%s: response %q, want %q and never a post-decode dimensions rejection", tc.name, response, tc.response)
		}
	}
}

func pngChunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

func craftedPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)

	var idat bytes.Buffer
	zw := zlib.NewWriter(&idat)
	if _, err := zw.Write(make([]byte, 32)); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}

	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("IDAT", idat.Bytes())...)
	return append(out, pngChunk("IEND", nil)...)
}

func kittyTransmitPNG(id uint32, data []byte) string {
	return fmt.Sprintf("\x1b_Ga=t,i=%d,f=100,t=d;%s\x1b\\", id, base64.StdEncoding.EncodeToString(data))
}

func encodeWidePNG(t *testing.T, w int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, 1))
	for x := 0; x < w; x++ {
		img.SetNRGBA(x, 0, color.NRGBA{R: uint8(x), G: 1, B: 2, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode(%dx1): %v", w, err)
	}
	return buf.Bytes()
}
