package garden

import "testing"

func seedWith(id string, edges ...Edge) Seed {
	return Seed{ID: id, Title: id, Status: StatusPlanted, Edges: edges}
}

func blocks(to string) Edge         { return Edge{Kind: EdgeBlocks, To: to} }
func partOf(to string) Edge         { return Edge{Kind: EdgePartOf, To: to} }
func discoveredFrom(to string) Edge { return Edge{Kind: EdgeDiscoveredFrom, To: to} }
func ids(seeds []Seed) []string {
	out := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		out = append(out, seed.ID)
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func noSession(string) bool { return false }

func TestUnblocks(t *testing.T) {
	closed := func(seed Seed) Seed { seed.Status = StatusHarvested; return seed }
	held := func(seed Seed, session, member string) Seed {
		seed.Status = StatusGrowing
		seed.TenderSession = session
		seed.TenderMember = member
		return seed
	}

	tests := []struct {
		name   string
		seeds  []Seed
		closed string
		want   []string
	}{
		{
			name:   "the one seed it was holding back",
			seeds:  []Seed{closed(seedWith("s-a", blocks("s-b"))), seedWith("s-b")},
			closed: "s-a",
			want:   []string{"s-b"},
		},
		{
			name: "every seed it was holding back",
			seeds: []Seed{
				closed(seedWith("s-a", blocks("s-b"), blocks("s-c"))), seedWith("s-b"), seedWith("s-c"),
			},
			closed: "s-a",
			want:   []string{"s-b", "s-c"},
		},
		{
			name:   "a seed a second blocker still holds is not free",
			seeds:  []Seed{closed(seedWith("s-a", blocks("s-c"))), seedWith("s-b", blocks("s-c")), seedWith("s-c")},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "withering frees what harvesting would",
			seeds:  []Seed{func() Seed { s := seedWith("s-a", blocks("s-b")); s.Status = StatusWithered; return s }(), seedWith("s-b")},
			closed: "s-a",
			want:   []string{"s-b"},
		},
		{
			name:   "a close that blocked nothing is quiet",
			seeds:  []Seed{closed(seedWith("s-a")), seedWith("s-b")},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "a held seed is announced: its tender is the one waiting on the blocker",
			seeds:  []Seed{closed(seedWith("s-a", blocks("s-b"))), held(seedWith("s-b"), "sess-1", "")},
			closed: "s-a",
			want:   []string{"s-b"},
		},
		{
			name:   "a parked dependent stays put — parking is a pause, not a wait",
			seeds:  []Seed{closed(seedWith("s-a", blocks("s-b"))), func() Seed { s := seedWith("s-b"); s.Status = StatusDormant; return s }()},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "a dependent closed on its own is not news",
			seeds:  []Seed{closed(seedWith("s-a", blocks("s-b"))), closed(seedWith("s-b"))},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "a gated dependent still wants a person",
			seeds:  []Seed{closed(seedWith("s-a", blocks("s-b"))), func() Seed { s := seedWith("s-b"); s.Gate = true; return s }()},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "a plot the close freed is not work to pick up",
			seeds:  []Seed{closed(seedWith("s-a", blocks("s-crown"))), seedWith("s-crown"), seedWith("s-child", partOf("s-crown"))},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "the other kinds of edge free nobody",
			seeds:  []Seed{closed(seedWith("s-a", partOf("s-crown"), discoveredFrom("s-b"))), seedWith("s-b"), seedWith("s-crown")},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "an open seed frees nothing: the ripple is read after the close",
			seeds:  []Seed{seedWith("s-a", blocks("s-b")), seedWith("s-b")},
			closed: "s-a",
			want:   []string{},
		},
		{
			name:   "a seed nobody planted frees nothing",
			seeds:  []Seed{seedWith("s-b")},
			closed: "s-gone",
			want:   []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ids(Unblocks(test.seeds, test.closed))
			if !equal(got, test.want) {
				t.Fatalf("Unblocks(%s) = %v, want %v", test.closed, got, test.want)
			}
		})
	}
}

func TestReady(t *testing.T) {
	held := func(seed Seed, session, member string) Seed {
		seed.Status = StatusGrowing
		seed.TenderSession = session
		seed.TenderMember = member
		return seed
	}

	tests := []struct {
		name  string
		seeds []Seed
		live  func(string) bool
		want  []string
	}{
		{
			name:  "a plain planted seed is ready",
			seeds: []Seed{seedWith("s-a")},
			want:  []string{"s-a"},
		},
		{
			name:  "an unclosed blocker holds its dependent back",
			seeds: []Seed{seedWith("s-a", blocks("s-b")), seedWith("s-b")},
			want:  []string{"s-a"},
		},
		{
			name: "harvesting the blocker surfaces the dependent",
			seeds: []Seed{
				func() Seed { s := seedWith("s-a", blocks("s-b")); s.Status = StatusHarvested; return s }(),
				seedWith("s-b"),
			},
			want: []string{"s-b"},
		},
		{
			name: "a withered blocker stops blocking too",
			seeds: []Seed{
				func() Seed { s := seedWith("s-a", blocks("s-b")); s.Status = StatusWithered; return s }(),
				seedWith("s-b"),
			},
			want: []string{"s-b"},
		},
		{
			name: "a parked blocker still blocks — parking is a pause, not an answer",
			seeds: []Seed{
				func() Seed { s := seedWith("s-a", blocks("s-b")); s.Status = StatusDormant; return s }(),
				seedWith("s-b"),
			},
			want: []string{},
		},
		{
			name:  "every blocker must go, not just one",
			seeds: []Seed{seedWith("s-a", blocks("s-c")), seedWith("s-b", blocks("s-c")), seedWith("s-c")},
			want:  []string{"s-a", "s-b"},
		},
		{
			name:  "a crown is not ready — its work is its children",
			seeds: []Seed{seedWith("s-child", partOf("s-crown")), seedWith("s-crown")},
			want:  []string{"s-child"},
		},
		{
			name: "a crown stays out of ready when its children close: it is finished by harvesting it, not by tending it",
			seeds: []Seed{
				func() Seed { s := seedWith("s-child", partOf("s-crown")); s.Status = StatusHarvested; return s }(),
				seedWith("s-crown"),
			},
			want: []string{},
		},
		{
			name:  "the chain the plan names: A blocks B, B part-of C leaves only A",
			seeds: []Seed{seedWith("s-a", blocks("s-b")), seedWith("s-b", partOf("s-c")), seedWith("s-c")},
			want:  []string{"s-a"},
		},
		{
			name:  "a closed seed is never ready",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Status = StatusHarvested; return s }()},
			want:  []string{},
		},
		{
			name:  "a parked seed is never ready",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Status = StatusDormant; return s }()},
			want:  []string{},
		},
		{
			name:  "a gate wants a person, not an agent picking up work",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Gate = true; return s }()},
			want:  []string{},
		},
		{
			name:  "a packet is a shape waiting to be sown",
			seeds: []Seed{func() Seed { s := seedWith("s-a"); s.Template = true; return s }()},
			want:  []string{},
		},
		{
			name: "a seed under a packet is not work either",
			seeds: []Seed{
				func() Seed { s := seedWith("s-packet"); s.Template = true; return s }(),
				seedWith("s-step", partOf("s-packet")),
			},
			want: []string{},
		},
		{
			name:  "a live session holds its seed",
			seeds: []Seed{held(seedWith("s-a"), "sess-1", "")},
			live:  func(id string) bool { return id == "sess-1" },
			want:  []string{},
		},
		{
			name:  "a session the daemon no longer knows releases its seed",
			seeds: []Seed{held(seedWith("s-a"), "sess-gone", "")},
			live:  func(string) bool { return false },
			want:  []string{"s-a"},
		},
		{
			name:  "a member-only tender always holds: attn cannot tell that a person walked away",
			seeds: []Seed{held(seedWith("s-a"), "", "victor")},
			live:  func(string) bool { return false },
			want:  []string{},
		},
		{
			name:  "a stale tender name on an untended seed does not hold it",
			seeds: []Seed{seedWith("s-a")},
			want:  []string{"s-a"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			live := test.live
			if live == nil {
				live = noSession
			}
			got := ids(Ready(test.seeds, live))
			if !equal(got, test.want) {
				t.Fatalf("Ready = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTree(t *testing.T) {
	tests := []struct {
		name   string
		seeds  []Seed
		rows   []string
		depths []int
	}{
		{
			name:   "children follow their crown",
			seeds:  []Seed{seedWith("s-crown"), seedWith("s-one", partOf("s-crown")), seedWith("s-two", partOf("s-crown"))},
			rows:   []string{"s-crown", "s-one", "s-two"},
			depths: []int{0, 1, 1},
		},
		{
			name:   "a child listed before its crown still lands under it",
			seeds:  []Seed{seedWith("s-child", partOf("s-crown")), seedWith("s-crown")},
			rows:   []string{"s-crown", "s-child"},
			depths: []int{0, 1},
		},
		{
			name: "depth follows the chain",
			seeds: []Seed{
				seedWith("s-crown"),
				seedWith("s-mid", partOf("s-crown")),
				seedWith("s-leaf", partOf("s-mid")),
			},
			rows:   []string{"s-crown", "s-mid", "s-leaf"},
			depths: []int{0, 1, 2},
		},
		{
			name:   "a child whose crown is out of scope renders at the top rather than vanishing",
			seeds:  []Seed{seedWith("s-child", partOf("s-elsewhere"))},
			rows:   []string{"s-child"},
			depths: []int{0},
		},
		{
			name:   "a stored cycle renders instead of recursing forever",
			seeds:  []Seed{seedWith("s-a", partOf("s-b")), seedWith("s-b", partOf("s-a"))},
			rows:   []string{"s-a", "s-b"},
			depths: []int{0, 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows := Tree(test.seeds)
			got := make([]string, 0, len(rows))
			depths := make([]int, 0, len(rows))
			for _, row := range rows {
				got = append(got, row.Seed.ID)
				depths = append(depths, row.Depth)
			}
			if !equal(got, test.rows) {
				t.Fatalf("Tree = %v, want %v", got, test.rows)
			}
			for i := range depths {
				if depths[i] != test.depths[i] {
					t.Fatalf("depths = %v, want %v", depths, test.depths)
				}
			}
		})
	}
}

func TestInPlot(t *testing.T) {
	seeds := []Seed{
		seedWith("s-leaf", partOf("s-mid")),
		seedWith("s-mid", partOf("s-crown")),
		seedWith("s-crown"),
		seedWith("s-outside"),
	}
	got := ids(InPlot(seeds, "s-crown"))
	if !equal(got, []string{"s-leaf", "s-mid", "s-crown"}) {
		t.Fatalf("InPlot = %v", got)
	}
	if got := ids(InPlot(seeds, "s-outside")); !equal(got, []string{"s-outside"}) {
		t.Fatalf("InPlot(leaf crown) = %v", got)
	}
}

func TestRelations(t *testing.T) {
	seeds := []Seed{
		seedWith("s-a", blocks("s-b")),
		seedWith("s-b", partOf("s-c")),
		seedWith("s-c"),
	}
	got := Relations(seeds, "s-b")
	want := []Relation{
		{Label: EdgePartOf, Seed: "s-c"},
		{Label: "blocked-by", Seed: "s-a"},
	}
	if len(got) != len(want) {
		t.Fatalf("Relations = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Relations = %v, want %v", got, want)
		}
	}
	if got := Relations(seeds, "s-c"); len(got) != 1 || got[0] != (Relation{Label: "has-part", Seed: "s-b"}) {
		t.Fatalf("Relations(crown) = %v", got)
	}
}

func TestBlockers(t *testing.T) {
	seeds := []Seed{
		seedWith("s-a", blocks("s-c")),
		func() Seed { s := seedWith("s-b", blocks("s-c")); s.Status = StatusHarvested; return s }(),
		seedWith("s-c"),
	}
	if got := Blockers(seeds, "s-c"); !equal(got, []string{"s-a"}) {
		t.Fatalf("Blockers = %v, want [s-a]", got)
	}
	if got := Blockers(seeds, "s-a"); len(got) != 0 {
		t.Fatalf("Blockers(unblocked) = %v", got)
	}
}
