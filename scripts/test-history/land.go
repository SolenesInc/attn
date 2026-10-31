package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/testhistory"
)

func landCommand(args []string) int {
	flags := flag.NewFlagSet("land", flag.ContinueOnError)
	mainRef := flags.String("main", "origin/main", "")
	unitsOut := flags.String("units-out", "", "")
	var groups groupFlags
	flags.Var(&groups, "group", "")
	if err := flags.Parse(args); err != nil || *unitsOut == "" || len(groups) == 0 || flags.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	context, err := loadContext(*mainRef)
	if err != nil {
		return fail(err)
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return fail(fmt.Errorf("git rev-parse HEAD: %v", err))
	}
	base, err := git("merge-base", *mainRef, head)
	if err != nil {
		return fail(fmt.Errorf("no merge base with %s: %v", *mainRef, err))
	}
	request, err := context.request(tree{revision: head}, base, groups)
	if err != nil {
		return fail(err)
	}
	fallback := ""
	if context.lastSync == nil {
		request = testhistory.SelectRequest{Groups: request.Groups, Inventory: request.Inventory}
		fallback = "no shared history: no sync has succeeded into " + context.spool + "; run go tool test-history sync"
	}
	selection := testhistory.Select(request)
	units := landingUnits(selection)
	if len(units) == 0 {
		return fail(errors.New("groups " + strings.Join(groups, " ") + " name no landing tests"))
	}
	if err := os.WriteFile(*unitsOut, []byte(strings.Join(units, "\n")+"\n"), 0o644); err != nil {
		return fail(err)
	}
	printLanding(os.Stdout, selectReport{Selection: selection, Freshness: context.freshness()}, head, fallback)
	return 0
}

func landingUnits(selection testhistory.Selection) []string {
	var units []string
	for _, choice := range selection.Choices {
		if choice.Status == testhistory.Selected || choice.Baseline && choice.Status == testhistory.Unavailable {
			units = append(units, choice.Unit)
		}
	}
	return units
}

func printLanding(out io.Writer, report selectReport, head, fallback string) {
	rule := fmt.Sprintf("the baseline, the tests it changes, and each test that failed on main until it passes on %d main revisions", report.ClearedAfter)
	if fallback != "" {
		rule = "the baseline, " + fallback
	}
	fmt.Fprintf(out, "landing tests for %s: %s; groups %s\n", short(head), rule, joinOrNone(report.Groups))
	printHistory(out, report.Freshness)
	baseline, cleared, missing := 0, 0, 0
	for _, choice := range report.Choices {
		switch {
		case choice.Status == testhistory.Cleared:
			cleared++
			continue
		case choice.Status == testhistory.Missing:
			missing++
			continue
		case choice.Baseline && choice.Status == testhistory.Selected:
			baseline++
			continue
		}
		label := strings.ToUpper(string(choice.Status))
		if choice.Status == testhistory.Selected {
			label = "ADDED"
		}
		fmt.Fprintf(out, "%-11s %s\n", label, choice.Unit)
		for _, reason := range choiceReasons(choice) {
			fmt.Fprintf(out, "            %s\n", reason)
		}
		if choice.Baseline && choice.Status == testhistory.Unavailable {
			fmt.Fprintln(out, "            requested anyway: every baseline line must name a test in the checkout")
		}
	}
	fmt.Fprintf(out, "baseline: %d units", baseline)
	if cleared > 0 {
		fmt.Fprintf(out, "; %d tests that failed on main have cleared", cleared)
	}
	if missing > 0 {
		fmt.Fprintf(out, "; %d that failed on main are gone from this checkout", missing)
	}
	if cleared+missing > 0 {
		fmt.Fprint(out, " (go tool test-history select lists them)")
	}
	fmt.Fprintln(out)
}
