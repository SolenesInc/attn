package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAChildReadsItsParentsOutlineAndOneSection(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	body := "Fence the garden before spring.\n\n## Decisions\n\n- D1. Cedar posts.\n- D2. A gate on the east side,\n  wide enough for the barrow.\n\n## Findings\n\nF1. The soil is clay.\n"
	payload, _ := json.Marshal(map[string]any{"title": "Fence the garden", "body": body, "children": []map[string]string{{"title": "Hang the gate"}}})
	plot := plantPlot(t, s, string(payload))
	crown, gate := plot.Crown.ID, plot.Children[0].ID

	shown := seedAs(t, s, "", "show", gate)
	requireOrder(t, "the child's show", shown,
		"\npart-of  "+crown,
		"\noutline of "+crown+"\n",
		"  Decisions  ", "D1, D2\n",
		"  Findings  ", "F1\n",
		"  read one part: attn seed show "+crown+" --section <heading|id>\n")
	if outline := showSeed(t, s, "", gate).ParentOutline; len(outline) != 2 || outline[0].Heading != "Decisions" ||
		strings.Join(outline[0].Ids, " ") != "D1 D2" || outline[1].Heading != "Findings" || outline[1].Bytes != len("## Findings\n\nF1. The soil is clay.") {
		t.Errorf("parent_outline is %+v, want Decisions (D1 D2) and Findings (F1)", outline)
	}
	if shown := seedAs(t, s, "", "show", crown); strings.Contains(shown, "outline of") {
		t.Errorf("a seed in no plot shows an outline:\n%s", shown)
	}
	if outline := showSeed(t, s, "", crown).ParentOutline; outline != nil {
		t.Errorf("a seed in no plot carries parent_outline %+v", outline)
	}

	if got, want := seedAs(t, s, "", "show", crown, "--section", "D2"),
		crown+"  Fence the garden\n\n## Decisions\n\n- D2. A gate on the east side,\n  wide enough for the barrow.\n"; got != want {
		t.Errorf("show --section D2 =\n%q\nwant\n%q", got, want)
	}
	if got := seedAs(t, s, "", "show", crown, "--section", "find"); !strings.HasSuffix(got, "\n\n## Findings\n\nF1. The soil is clay.\n") {
		t.Errorf("show --section find =\n%s", got)
	}
	refused := s.Attn("seed", "show", crown, "--section", "Budget")
	if refused.Code != 1 || refused.Stdout != "" ||
		!strings.Contains(refused.Stderr, `no part matches "Budget"; the parts are: Decisions D1, D2 · Findings F1`) {
		t.Errorf("an unknown section: exit %d, stdout %q, stderr %q", refused.Code, refused.Stdout, refused.Stderr)
	}
	var sectioned protocol.SeedShowResult
	s.Attn("seed", "show", crown, "--section", "D2", "--json").JSON(t, &sectioned)
	if sectioned.Seed.Body != body {
		t.Errorf("show --json --section carries body %q, want the whole body", sectioned.Seed.Body)
	}
}
