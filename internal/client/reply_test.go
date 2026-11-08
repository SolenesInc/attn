package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type splitReader struct {
	data  []byte
	sizes []int
}

func (r *splitReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	size := len(r.data)
	if len(r.sizes) > 0 {
		size, r.sizes = r.sizes[0], r.sizes[1:]
	}
	n := copy(p, r.data[:min(size, len(r.data))])
	r.data = r.data[n:]
	return n, nil
}

func replyValue(t *rapid.T) []byte {
	pattern := rapid.StringOfN(rapid.SampledFrom([]rune(`ab{}[]"\:, é✓`)), 1, 16, -1).Draw(t, "pattern")
	repeats := rapid.IntRange(0, 3*replyChunkBytes/len(pattern)).Draw(t, "repeats")
	rows := rapid.SliceOfN(rapid.StringOfN(rapid.SampledFrom([]rune(`x}]"\`)), 0, 64, -1), 0, 50)
	value, err := json.Marshal(map[string]any{
		"ok":   rapid.Bool().Draw(t, "ok"),
		"text": strings.Repeat(pattern, repeats),
		"rows": rows.Draw(t, "rows"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func readSizes(t *rapid.T) []int {
	return rapid.SliceOf(rapid.IntRange(1, 2*replyChunkBytes)).Draw(t, "read sizes")
}

func TestReadReplyReturnsExactlyTheFirstValueHoweverTheReadsSplitIt(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		value := replyValue(t)
		trailing := []byte(rapid.SampledFrom([]string{"", "\n", "\n{\"ok\":true}"}).Draw(t, "trailing"))
		got, err := readReply(&splitReader{data: append(bytes.Clone(value), trailing...), sizes: readSizes(t)})
		if err != nil {
			t.Fatalf("readReply: %v", err)
		}
		if !bytes.Equal(got, value) {
			t.Fatalf("readReply returned %d bytes, want the %d-byte value", len(got), len(value))
		}
	})
}

func TestReadReplyReportsAReplyCutShort(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		value := replyValue(t)
		cut := rapid.IntRange(1, len(value)-1).Draw(t, "cut")
		_, err := readReply(&splitReader{data: value[:cut], sizes: readSizes(t)})
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("readReply on %d of %d bytes = %v, want io.ErrUnexpectedEOF", cut, len(value), err)
		}
	})
	if _, err := readReply(&splitReader{}); !errors.Is(err, io.EOF) {
		t.Fatalf("readReply on a closed connection = %v, want io.EOF", err)
	}
}
