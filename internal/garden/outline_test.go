package garden

import (
	"os"
	"strings"
	"testing"
)

func readOutlineBody(t *testing.T, seedID string) string {
	t.Helper()
	body, err := os.ReadFile("testdata/outline/" + seedID + ".md")
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestOutlineListsHeadingsWithTheirItemIDs(t *testing.T) {
	for _, tc := range []struct {
		body string
		want string
	}{
		{"s-bhb3t4", "Evidence base · Findings by root cause F1–F8 · Sequence D1, D2 · Eval sketch · Related seeds"},
		{"s-37yjkj", "Terms · Decisions D1–D8 · Tradeoffs accepted D1, D3, D5, D6, D8 · Findings F1–F9 · Design · Execution · Completion · Alternatives rejected"},
		{"s-8kv1rm", "Terms · Decisions D1–D12 · Tradeoffs accepted D1, D2, D11, D12 · Findings F1–F10 · Design I1–I4 · Execution · Completion · Alternatives rejected D1–D3, D6, D12, F4"},
		{"inline:no headings", "none, the body has no headings"},
		{"inline:# Plan\n\n- **C131.** a check\n\n```md\n## not a heading\n- D9. not an item\n```\n\n## Next\n\n- D1: one\n  - D2. nested, not an item\n", "Plan C131 · Next D1"},
	} {
		body := tc.body
		if inline, ok := strings.CutPrefix(body, "inline:"); ok {
			body = inline
		} else {
			body = readOutlineBody(t, body)
		}
		if got := DescribeOutline(Outline(body)); got != tc.want {
			t.Errorf("outline of %.40q\n got %s\nwant %s", tc.body, got, tc.want)
		}
	}
}

func TestOutlineSizesAreTheSectionsSectionReturns(t *testing.T) {
	for _, seedID := range []string{"s-bhb3t4", "s-37yjkj", "s-8kv1rm"} {
		body := readOutlineBody(t, seedID)
		total := 0
		for _, part := range Outline(body) {
			section, err := Section(body, part.Heading)
			if err != nil || len(section) != part.Bytes {
				t.Fatalf("%s %q: outline says %d bytes, section is %d (%v)", seedID, part.Heading, part.Bytes, len(section), err)
			}
			total += part.Bytes
		}
		if total == 0 || total > len(body) {
			t.Fatalf("%s: sections hold %d of %d bytes", seedID, total, len(body))
		}
	}
}

func TestSectionReturnsOnePartOrListsTheParts(t *testing.T) {
	plot := readOutlineBody(t, "s-8kv1rm")
	sequence := readOutlineBody(t, "s-bhb3t4")
	closing := readOutlineBody(t, "s-37yjkj")
	for _, tc := range []struct {
		name, body, ref string
		starts, ends    string
		absent          []string
		err             string
	}{
		{name: "paragraph finding", body: sequence, ref: "F3",
			starts: "## Findings by root cause\n\nF3. Reading is not incremental.",
			ends:   `("i answered already").`, absent: []string{"F2.", "F4."}},
		{name: "paragraph finding keeps its list", body: sequence, ref: "F1",
			starts: "## Findings by root cause\n\nF1. Silent wrong results",
			ends:   "(44 failures, 27 never recovered).", absent: []string{"F2."}},
		{name: "paragraph finding stops at the next paragraph", body: sequence, ref: "F8",
			ends: "closed seeds included.", absent: []string{"What works and must survive"}},
		{name: "decision keeps its numbered steps", body: sequence, ref: "d1",
			starts: "## Sequence\n\nD1. Fix the interface", ends: "at equal task success.", absent: []string{"D2."}},
		{name: "list item under every heading that uses it", body: plot, ref: "D12",
			starts: "## Decisions\n\n- D12. When a bookmark is older",
			ends:   "which a full read already serves.",
			absent: []string{"The user confirmed D10", "- D11.", "## Findings"}},
		{name: "list item stops at its sibling", body: closing, ref: "D7",
			starts: "## Decisions\n\n- D7. Every lifecycle refusal is one line", absent: []string{"- D8."}},
		{name: "heading by prefix", body: plot, ref: "trade",
			starts: "## Tradeoffs accepted\n", ends: "one direction word in plot payloads.", absent: []string{"## Findings"}},
		{name: "heading keeps its fenced code", body: plot, ref: "Design",
			starts: "## Design\n", ends: "a second conversion finds none", absent: []string{"## Execution"}},
		{name: "exact heading beats a longer one", body: "## Design notes\n\nlong\n\n## Design\n\nshort\n", ref: "design",
			starts: "## Design\n\nshort"},
		{name: "ambiguous prefix", body: plot, ref: "de", err: `"de" begins 2 headings: Decisions, Design`},
		{name: "unknown heading", body: sequence, ref: "Budget",
			err: `no part matches "Budget"; the parts are: Evidence base · Findings by root cause F1–F8 · Sequence D1, D2 · Eval sketch · Related seeds`},
		{name: "unknown item", body: closing, ref: "F12", err: "no item starts with F12; the parts are: Terms · Decisions D1–D8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Section(tc.body, tc.ref)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Section(%q) error = %v, want %q", tc.ref, err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Section(%q): %v", tc.ref, err)
			}
			if !strings.HasPrefix(got, tc.starts) || !strings.HasSuffix(got, tc.ends) {
				t.Fatalf("Section(%q) =\n%s\n\nwant it to start %q and end %q", tc.ref, got, tc.starts, tc.ends)
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Fatalf("Section(%q) holds %q:\n%s", tc.ref, absent, got)
				}
			}
		})
	}
}

func TestIDRangesFoldRunsPerLetter(t *testing.T) {
	for ids, want := range map[string]string{
		"":                       "",
		"D1":                     "D1",
		"D2 D1":                  "D1, D2",
		"D1 D2 D3 D5 F1":         "D1–D3, D5, F1",
		"F4 D3 D1 D2 D6 D12 D11": "F4, D1–D3, D6, D11, D12",
		"C131 C133 C132 C135":    "C131–C133, C135",
	} {
		if got := IDRanges(strings.Fields(ids)); got != want {
			t.Errorf("IDRanges(%s) = %q, want %q", ids, got, want)
		}
	}
}
