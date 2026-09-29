package transcript

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestReadingLinesBackwardYieldsTheForwardLinesReversed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var file bytes.Buffer
		for i, n := 0, rapid.IntRange(0, 12).Draw(t, "lines"); i < n; i++ {
			size := rapid.SampledFrom([]int{0, 1, 40, reverseReadChunk - 1, reverseReadChunk, reverseReadChunk + 1, 3 * reverseReadChunk}).Draw(t, "size")
			file.WriteString(strings.Repeat(string(rune('a'+i%26)), size))
			file.WriteString(rapid.SampledFrom([]string{"\n", "\r\n", " \n"}).Draw(t, "end"))
		}
		if rapid.Bool().Draw(t, "unterminated") {
			file.WriteString("tail")
		}

		var forward, backward []string
		if err := readJSONLLines(bytes.NewReader(file.Bytes()), func(line []byte) { forward = append(forward, string(line)) }); err != nil {
			t.Fatal(err)
		}
		if err := readJSONLLinesReverse(bytes.NewReader(file.Bytes()), int64(file.Len()), func(line []byte) bool {
			backward = append(backward, string(line))
			return false
		}); err != nil {
			t.Fatal(err)
		}
		slices.Reverse(backward)
		if !slices.Equal(forward, backward) {
			t.Fatalf("backward read %d lines, forward %d", len(backward), len(forward))
		}
	})
}
