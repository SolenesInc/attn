package pty

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

type segMarker struct {
	Kind     string  `json:"kind"`
	Cmdline  *string `json:"cmdline,omitempty"`
	ExitCode *int    `json:"exitCode,omitempty"`
}

type segCase struct {
	Name    string      `json:"name"`
	Chunks  []string    `json:"chunks"`
	Markers []segMarker `json:"markers"`
}

func loadSegCorpus(t *testing.T) []segCase {
	t.Helper()
	data, err := os.ReadFile("testdata/osc133_segmenter_corpus.json")
	if err != nil {
		t.Fatalf("read segmenter corpus: %v", err)
	}
	var doc struct {
		Cases []segCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse segmenter corpus: %v", err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("segmenter corpus is empty")
	}
	return doc.Cases
}

func markerToSeg(m *osc133Marker) segMarker {
	sm := segMarker{}
	switch m.Kind {
	case osc133PromptStart:
		sm.Kind = "prompt-start"
	case osc133InputStart:
		sm.Kind = "input-start"
	case osc133PreExec:
		sm.Kind = "pre-exec"
		sm.Cmdline = m.Cmdline
	case osc133CommandEnd:
		sm.Kind = "command-end"
		if m.ExitCode != nil {
			v := int(*m.ExitCode)
			sm.ExitCode = &v
		}
	}
	return sm
}

func collectMarkers(seg *feedSegmenter, chunks []string) (markers []segMarker, plain []byte) {
	var buf bytes.Buffer
	for _, chunk := range chunks {
		seg.Feed([]byte(chunk), func(s feedSegment) {
			if s.Kind == feedSegPlain {
				buf.Write(s.Bytes)
			}
			if s.Marker != nil {
				markers = append(markers, markerToSeg(s.Marker))
			}
		})
	}
	return markers, buf.Bytes()
}

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func markersEqual(got, want []segMarker) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Kind != want[i].Kind ||
			!strPtrEq(got[i].Cmdline, want[i].Cmdline) ||
			!intPtrEq(got[i].ExitCode, want[i].ExitCode) {
			return false
		}
	}
	return true
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The shared corpus is the only thing holding the client's parseOsc133 equal to
// this one.
func TestOsc133SegmenterCorpus(t *testing.T) {
	for _, c := range loadSegCorpus(t) {
		t.Run(c.Name, func(t *testing.T) {
			seg := &feedSegmenter{}
			got, plain := collectMarkers(seg, c.Chunks)
			if !markersEqual(got, c.Markers) {
				t.Fatalf("markers mismatch\n got: %s\nwant: %s", mustJSON(got), mustJSON(c.Markers))
			}
			if bytes.Contains(plain, osc133Prefix) {
				t.Fatalf("plain output still contains an OSC 133 introducer: %q", plain)
			}
		})
	}
}

func TestOsc133SegmenterByteSplitting(t *testing.T) {
	for _, c := range loadSegCorpus(t) {
		t.Run(c.Name, func(t *testing.T) {
			var singleByte []string
			for _, chunk := range c.Chunks {
				for i := 0; i < len(chunk); i++ {
					singleByte = append(singleByte, chunk[i:i+1])
				}
			}
			got, plain := collectMarkers(&feedSegmenter{}, singleByte)
			if !markersEqual(got, c.Markers) {
				t.Fatalf("byte-split markers mismatch\n got: %s\nwant: %s", mustJSON(got), mustJSON(c.Markers))
			}
			if bytes.Contains(plain, osc133Prefix) {
				t.Fatalf("byte-split plain output contains an OSC 133 introducer: %q", plain)
			}
		})
	}
}

func TestOsc133SegmenterBrokenMarkerPassesThrough(t *testing.T) {
	seg := &feedSegmenter{}
	broken := append([]byte{oscESC}, []byte("]133;C;cmdline_url=")...)
	broken = append(broken, bytes.Repeat([]byte("x"), osc133MarkerMaxPendingBytes+16)...)

	var markers int
	var plain bytes.Buffer
	seg.Feed(broken, func(s feedSegment) {
		plain.Write(s.Bytes)
		if s.Marker != nil {
			markers++
		}
	})
	if markers != 0 {
		t.Fatalf("broken marker produced %d markers, want 0", markers)
	}
	if plain.Len() != len(broken) {
		t.Fatalf("broken marker: flushed %d bytes, want %d (all passed through)", plain.Len(), len(broken))
	}
}
