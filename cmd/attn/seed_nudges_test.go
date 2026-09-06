package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedNoteRingFlagIsExplicit(t *testing.T) {
	f := newSeedFlags("note")
	positionals := f.parse("note", []string{"s-7k3f9m", "-m", "look here", "--ring"})
	if len(positionals) != 1 || positionals[0] != "s-7k3f9m" || !*f.ring {
		t.Fatalf("parsed note = positionals %q ring %v", positionals, *f.ring)
	}

	plain := newSeedFlags("note")
	plain.parse("note", []string{"s-7k3f9m", "-m", "quiet"})
	if *plain.ring {
		t.Fatal("a plain note rings without --ring")
	}
}

func TestSeedShowRendersAndSerializesWatchState(t *testing.T) {
	result := &protocol.SeedShowResult{
		Seed:     protocol.Seed{ID: "s-7k3f9m", Title: "doorbell", Status: "growing"},
		Watching: true,
	}
	var out bytes.Buffer
	fprintSeedShow(&out, result)
	if !strings.Contains(out.String(), "watching  yes") {
		t.Fatalf("show output has no watch state:\n%s", out.String())
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"watching":true`) {
		t.Fatalf("JSON has no watch state: %s", raw)
	}
}

func TestSeedWatchResultExplainsRemainingCoverage(t *testing.T) {
	tests := []struct {
		name    string
		result  protocol.SeedWatchResult
		unwatch bool
		want    []string
	}{
		{"new watch", protocol.SeedWatchResult{SeedID: "child", Watching: true, Changed: true, WatchingVia: []string{"child"}}, false, []string{"watching child and its descendants"}},
		{"removed direct with ancestor", protocol.SeedWatchResult{SeedID: "child", Watching: true, Changed: true, WatchingVia: []string{"parent"}}, true, []string{"removed watch on child", "watching via parent", "attn seed unwatch parent"}},
		{"inherited only", protocol.SeedWatchResult{SeedID: "child", Watching: true, WatchingVia: []string{"parent"}}, true, []string{"no direct watch on child", "attn seed unwatch parent"}},
		{"removed last watch", protocol.SeedWatchResult{SeedID: "parent", Changed: true, WatchingVia: []string{}}, true, []string{"removed watch on parent", "no remaining watch covers parent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			fprintSeedWatchResult(&out, &tt.result, tt.unwatch)
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("output %q lacks %q", out.String(), want)
				}
			}
		})
	}
}

func TestSeedShowNamesInheritedSubscriptionsInTextAndJSON(t *testing.T) {
	result := &protocol.SeedShowResult{Seed: protocol.Seed{ID: "child", Title: "child", Status: "growing"}, Watching: true, WatchingVia: []string{"parent"}}
	var out bytes.Buffer
	fprintSeedShow(&out, result)
	if !strings.Contains(out.String(), "watching via parent (remove with attn seed unwatch parent)") {
		t.Fatalf("show lacks reversal: %s", out.String())
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"watching_via":["parent"]`) {
		t.Fatalf("JSON lacks coverage: %s", raw)
	}
}
