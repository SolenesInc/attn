package main_test

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAViewsClipReachesTheAppFromTheHomeAndFromAnOutpost(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	clip := silentWav(2)
	for _, side := range []struct {
		name string
		host *string
		path string
	}{
		{"home", nil, filepath.Join(home.Path("check"), "clips", "03.wav")},
		{"outpost", protocol.Ptr(pair.Host), filepath.Join(outpost.Path("check"), "clips", "03.wav")},
	} {
		if err := os.MkdirAll(filepath.Dir(side.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(side.path, clip, 0o644); err != nil {
			t.Fatal(err)
		}
		id := "clip-" + side.name
		read := testworld.Request(pair.App, protocol.TileAssetReadMessage{
			Cmd: protocol.CmdTileAssetRead, Host: side.host, Path: side.path, RequestID: protocol.Ptr(id),
		}, protocol.EventTileAssetReadResult, func(r protocol.TileAssetReadResultMessage) bool {
			return protocol.Deref(r.RequestID) == id
		})
		want := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(clip)
		if !read.Success || protocol.Deref(read.DataUri) != want {
			t.Fatalf("the clip on the %s reached the app as success=%v error=%q, %d bytes of data URI; want the clip as audio/wav",
				side.name, read.Success, protocol.Deref(read.Error), len(protocol.Deref(read.DataUri)))
		}
	}
}

// silentWav is a WAV file of `seconds` of silence, 8 kHz mono 16-bit.
func silentWav(seconds int) []byte {
	const rate, bytesPerSample = 8000, 2
	data := rate * bytesPerSample * seconds
	header := []any{
		[4]byte{'R', 'I', 'F', 'F'}, uint32(36 + data), [4]byte{'W', 'A', 'V', 'E'},
		[4]byte{'f', 'm', 't', ' '}, uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * bytesPerSample), uint16(bytesPerSample), uint16(16),
		[4]byte{'d', 'a', 't', 'a'}, uint32(data),
	}
	var out []byte
	for _, field := range header {
		out, _ = binary.Append(out, binary.LittleEndian, field)
	}
	return append(out, make([]byte, data)...)
}
