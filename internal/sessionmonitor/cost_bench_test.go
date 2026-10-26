package sessionmonitor

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func BenchmarkMonitorCostView(b *testing.B) {
	path := os.Getenv("ATTN_MONITOR_COST_FIXTURE")
	if path == "" {
		b.Skip("set ATTN_MONITOR_COST_FIXTURE to views written by scripts/monitorcostprobe -fixture")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	var views []json.RawMessage
	if err := json.Unmarshal(raw, &views); err != nil {
		b.Fatal(err)
	}
	slices.SortFunc(views, func(a, b json.RawMessage) int { return len(a) - len(b) })
	for name, input := range map[string]json.RawMessage{"p50": views[(len(views)-1)/2], "largest": views[len(views)-1]} {
		b.Run(name, func(b *testing.B) {
			var view View
			if err := json.Unmarshal(input, &view); err != nil {
				b.Fatal(err)
			}
			wire, err := json.Marshal(view)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("encode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(wire)))
				for b.Loop() {
					if _, err := json.Marshal(view); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("decode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(wire)))
				for b.Loop() {
					var decoded View
					if err := json.Unmarshal(wire, &decoded); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("unchanged_cost", func(b *testing.B) {
				var decoded View
				if err := json.Unmarshal(wire, &decoded); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					CostChange(view, decoded)
				}
			})
		})
	}
}
