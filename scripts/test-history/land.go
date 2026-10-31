package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/testhistory"
)

var landingPolicy = struct {
	policy      testhistory.Policy
	mode        testhistory.Mode
	allowanceMS int64
}{testhistory.SuccessCount, testhistory.ModeExtend, 0}

func landCommand(args []string) int {
	flags := flag.NewFlagSet("land", flag.ContinueOnError)
	mainRef := flags.String("main", "origin/main", "")
	unitsOut := flags.String("units-out", "", "")
	if err := flags.Parse(args); err != nil || *unitsOut == "" || flags.NArg() != 0 {
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
	policy, mode, allowance := landingPolicy.policy, landingPolicy.mode, &landingPolicy.allowanceMS
	fallback := ""
	if context.freshness().LastSync == nil {
		mode, allowance = testhistory.ModeBaseline, nil
		fallback = "no shared history: no sync has succeeded into " + context.spool + "; run go tool test-history sync"
	}
	request, err := context.request(tree{revision: head}, base, policy, mode, allowance)
	if err != nil {
		return fail(err)
	}
	selection := testhistory.Select(request)
	var units strings.Builder
	for _, unit := range landingUnits(selection) {
		units.WriteString(unit + "\n")
	}
	if err := os.WriteFile(*unitsOut, []byte(units.String()), 0o644); err != nil {
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
	mode := fmt.Sprintf("%s, the baseline plus ranked tests within its cost", report.Policy)
	if fallback != "" {
		mode = "the baseline, " + fallback
	}
	fmt.Fprintf(out, "landing tests for %s: %s; groups %s\n", short(head), mode, joinOrNone(report.Groups))
	printHistory(out, report.Freshness)
	for _, budget := range report.Budgets {
		printBudget(out, budget)
	}
	baseline := 0
	for _, choice := range report.Choices {
		if choice.Baseline && choice.Status == testhistory.Selected {
			baseline++
			continue
		}
		label := strings.ToUpper(string(choice.Status))
		if choice.Status == testhistory.Selected {
			label = "ADDED"
		}
		fmt.Fprintf(out, "%-11s %s", label, choice.Unit)
		if choice.CostMS != nil {
			fmt.Fprintf(out, " (%s)", seconds(*choice.CostMS))
		}
		fmt.Fprintln(out)
		for _, reason := range choiceReasons(choice) {
			fmt.Fprintf(out, "            %s\n", reason)
		}
		if choice.Baseline && choice.Status == testhistory.Unavailable {
			fmt.Fprintln(out, "            requested anyway: every baseline line must name a test in the checkout")
		}
	}
	fmt.Fprintf(out, "baseline: %d units\n", baseline)
}
