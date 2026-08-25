package pty

import "testing"

func applyAndCount(t *testing.T, markers []osc133Marker) (created, freed int) {
	t.Helper()
	bt := newBlockTable()
	for i, m := range markers {
		created++
		bt.ApplyMarker(m, &fakeBlockRef{x: 0, y: i, freed: &freed}, false)
	}
	bt.Close()
	return created, freed
}

func TestBlockTableRepeatedMarkersFreeReplacedRefs(t *testing.T) {
	cmd := "echo hi"
	zero := int32(0)

	cases := []struct {
		name    string
		markers []osc133Marker
	}{
		{
			name: "repeated prompt-start",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133PromptStart},
				{Kind: osc133PromptStart},
			},
		},
		{
			name: "repeated input-start",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133InputStart},
				{Kind: osc133InputStart},
				{Kind: osc133InputStart},
			},
		},
		{
			name: "repeated pre-exec",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133InputStart},
				{Kind: osc133PreExec, Cmdline: &cmd},
				{Kind: osc133PreExec, Cmdline: &cmd},
			},
		},
		{
			name: "every marker doubled",
			markers: []osc133Marker{
				{Kind: osc133PromptStart},
				{Kind: osc133PromptStart},
				{Kind: osc133InputStart},
				{Kind: osc133InputStart},
				{Kind: osc133PreExec, Cmdline: &cmd},
				{Kind: osc133PreExec, Cmdline: &cmd},
				{Kind: osc133CommandEnd, ExitCode: &zero},
				{Kind: osc133CommandEnd, ExitCode: &zero},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, freed := applyAndCount(t, tc.markers)
			if freed != created {
				t.Fatalf("freed %d of %d refs after Close; %d leaked", freed, created, created-freed)
			}
		})
	}
}

func TestBlockTableRepeatedMarkersDoNotDoubleFree(t *testing.T) {
	cmd := "echo hi"
	created, freed := applyAndCount(t, []osc133Marker{
		{Kind: osc133PromptStart},
		{Kind: osc133PromptStart},
		{Kind: osc133InputStart},
		{Kind: osc133InputStart},
		{Kind: osc133PreExec, Cmdline: &cmd},
		{Kind: osc133PreExec, Cmdline: &cmd},
	})
	if freed > created {
		t.Fatalf("freed %d refs but only %d were created: %d double-free(s)", freed, created, freed-created)
	}
	if freed != created {
		t.Fatalf("freed %d of %d refs after Close; %d leaked", freed, created, created-freed)
	}
}

func TestBlockTableRepeatedMarkersKeepLatestPosition(t *testing.T) {
	cmd := "echo hi"
	zero := int32(0)
	freed := 0
	bt := newBlockTable()

	steps := []struct {
		marker osc133Marker
		row    int
	}{
		{osc133Marker{Kind: osc133PromptStart}, 1},
		{osc133Marker{Kind: osc133PromptStart}, 5},
		{osc133Marker{Kind: osc133InputStart}, 6},
		{osc133Marker{Kind: osc133InputStart}, 7},
		{osc133Marker{Kind: osc133PreExec, Cmdline: &cmd}, 8},
		{osc133Marker{Kind: osc133CommandEnd, ExitCode: &zero}, 9},
	}
	for _, s := range steps {
		bt.ApplyMarker(s.marker, &fakeBlockRef{x: 0, y: s.row, freed: &freed}, false)
	}

	snap := bt.SnapshotBlocks()
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d blocks, want 1: %s", len(snap), mustJSON(snap))
	}
	got := snap[0]
	if got.PromptRow != 5 {
		t.Fatalf("promptRow = %d, want 5 (the redrawn prompt, not the stale row 1)", got.PromptRow)
	}
	if got.InputRow == nil || *got.InputRow != 7 {
		t.Fatalf("inputRow = %s, want 7 (the re-pinned input)", mustJSON(got.InputRow))
	}
	if got.OutputStartRow == nil || *got.OutputStartRow != 8 {
		t.Fatalf("outputStartRow = %s, want 8", mustJSON(got.OutputStartRow))
	}

	bt.Close()
	if freed != len(steps) {
		t.Fatalf("freed %d of %d refs after Close; %d leaked", freed, len(steps), len(steps)-freed)
	}
}
