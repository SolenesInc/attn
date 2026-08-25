//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// fuzzKittyMaxInput: real transmissions chunk at 4 KiB; 64 KiB clears any single
// escape a seed can grow into, without walking the segmenter to its 72 MiB tripwire.
const fuzzKittyMaxInput = 64 << 10

const (
	fuzzKittyCols = 20
	fuzzKittyRows = 8
)

var fuzzKittyFlush = []byte("\x1b\\")

func FuzzKittyWireMirrorShipping(f *testing.F) {
	fuzzKittyWireMirror(f, 0)
}

// Measured on the pin in ghostty-vt.pin: 15m / 38.1M execs green, then the deferred-wrap
// class 97s into the next soak, then 15m / 42.5M execs green again with its tripwire in.
func FuzzKittyWireMirror(f *testing.F) {
	fuzzKittyWireMirror(f, mirrorStorageLimit)
}

func fuzzKittyWireMirror(f *testing.F, storageLimit uint64) {
	for _, in := range kittyCorpusInputs() {
		f.Add([]byte(strings.Join(in.chunks, "")), uint16(len(in.chunks[0])))
	}
	for _, tc := range mirrorCases {
		f.Add([]byte(strings.Join(tc.chunks, "")), uint16(64))
	}

	f.Fuzz(func(t *testing.T, data []byte, chunkSize uint16) {
		if len(data) > fuzzKittyMaxInput {
			data = data[:fuzzKittyMaxInput]
		}
		size := int(chunkSize%4096) + 1
		baseline := ghosttyvt.LiveTrackedRefs()
		worker := newKittyTerminal(t, fuzzKittyCols, fuzzKittyRows, ghosttyvt.Options{KittyImageStorageLimit: storageLimit})
		client := newKittyTerminal(t, fuzzKittyCols, fuzzKittyRows, ghosttyvt.Options{})
		feeder := newWireFeeder(worker, 0, nil, 0)
		if feeder == nil {
			t.Fatalf("newWireFeeder returned nil for a live terminal")
		}

		resynced := ""
		feed := func(chunk []byte) {
			wire, resync := feeder.feed(chunk)
			writeAsClient(client, wire)
			if resync != "" && resynced == "" {
				resynced = resync
			}
		}
		for start := 0; start < len(data); start += size {
			feed(data[start:min(start+size, len(data))])
		}
		feed(fuzzKittyFlush)

		if resynced == "" {
			if got, want := client.PlainText(), worker.PlainText(); got != want {
				t.Errorf("history diverged with no resync (chunk size %d)\nworker:\n%s\nclient:\n%s", size, want, got)
			}
			if got, want := client.ViewportText(), worker.ViewportText(); got != want {
				t.Errorf("viewport diverged with no resync (chunk size %d)\nworker:\n%s\nclient:\n%s", size, want, got)
			}
			wx, wy := worker.CursorPos()
			cx, cy := client.CursorPos()
			if wx != cx || wy != cy {
				t.Errorf("cursor diverged with no resync (chunk size %d): client (%d,%d), worker (%d,%d)", size, cx, cy, wx, wy)
			}
		}

		feeder.close()
		if got := ghosttyvt.LiveTrackedRefs(); got != baseline {
			t.Errorf("LiveTrackedRefs() = %d after the run, want the %d it started at", got, baseline)
		}
	})
}

func FuzzKittySegmenterFraming(f *testing.F) {
	for _, c := range kittySegBattery {
		f.Add([]byte(c.input), uint16(3))
	}
	for _, in := range kittyCorpusInputs() {
		f.Add([]byte(strings.Join(in.chunks, "")), uint16(len(in.chunks[0])))
	}
	for _, prefix := range kittyGroundNamedPrefixes {
		f.Add([]byte(prefix), uint16(1))
	}

	f.Fuzz(func(t *testing.T, data []byte, chunkSize uint16) {
		if len(data) > fuzzKittyMaxInput {
			data = data[:fuzzKittyMaxInput]
		}
		size := int(chunkSize%4096) + 1

		var seg feedSegmenter
		rebuilt := make([]byte, 0, len(data))
		for start := 0; start < len(data); start += size {
			chunk := data[start:min(start+size, len(data))]
			// A copy per chunk: an emission may alias the chunk, and the
			// segmenter is allowed to reuse its own buffer afterwards.
			seg.Feed(append([]byte(nil), chunk...), func(e feedSegment) {
				rebuilt = append(rebuilt, e.Bytes...)
			})
		}
		if got := string(rebuilt) + string(seg.pending); got != string(data) {
			t.Fatalf("emissions rebuild %q, want %q (chunk size %d)", got, data, size)
		}

		want := ghosttyInGround(t, string(data))
		got := seg.mode == kittySegGround && len(seg.pending) == 0
		if got != want {
			t.Errorf("after %q (chunk size %d): segmenter ground=%v, ghostty ground=%v", data, size, got, want)
		}
	})
}
