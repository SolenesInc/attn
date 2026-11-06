package main_test

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

const maxPackageInitAllocs = 13000

var packageInitLine = regexp.MustCompile(`(?m)^init (\S+) @.* (\d+) allocs$`)

func TestEveryCommandStartsWithoutPayingForInitItNeverUses(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	started := s.Run(testworld.Invocation{Args: []string{"version"}, Env: []string{"GODEBUG=inittrace=1"}})

	type packageInit struct {
		name   string
		allocs int
	}
	var inits []packageInit
	total := 0
	for _, line := range packageInitLine.FindAllStringSubmatch(started.Stderr, -1) {
		allocs, err := strconv.Atoi(line[2])
		if err != nil {
			t.Fatal(err)
		}
		inits = append(inits, packageInit{line[1], allocs})
		total += allocs
	}
	if len(inits) == 0 {
		t.Fatalf("GODEBUG=inittrace=1 printed no package init lines to read:\n%s", started.Stderr)
	}
	if total <= maxPackageInitAllocs {
		return
	}
	slices.SortFunc(inits, func(a, b packageInit) int { return cmp.Compare(b.allocs, a.allocs) })
	var heaviest strings.Builder
	for _, init := range inits[:min(8, len(inits))] {
		fmt.Fprintf(&heaviest, "  %d %s\n", init.allocs, init.name)
	}
	t.Fatalf("attn's package init makes %d allocations, past maxPackageInitAllocs=%d. Every CLI command and agent hook pays this on every start. Move the work into the code path that needs it, or raise the limit with a new receipt as docs/internal/perf-testing.md says. Heaviest packages:\n%s", total, maxPackageInitAllocs, heaviest.String())
}
