package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/testhistory"
)

const usage = `usage: go tool test-history <command>

  capture <runner> [--unit <unit>]... [--shard <shard>] [--receipt-out <file>] -- <command>
      Run a test command and seal a receipt of what it observed into the spool.
      Runners: go, vitest, playwright, native. For go, the command is the
      arguments to go test. The exit status is the command's own.
      --unit names a requested unit; one that reports nothing is MISSING.
      --shard labels the receipt; it defaults to the command's --shard.
      --receipt-out also writes the sealed receipt to a file.
  recover
      Seal the working files of invocations that ended without sealing.
  summary <receipt.json>
      Print one line per requested unit (or reported unit, when none was
      requested); exit 1 unless each passed or skipped.
  sync
      Upload sealed receipts the history service has not acknowledged, then
      download the receipts it holds for this repository into the spool's
      shared directory. Prints what waits to upload and when a sync last
      succeeded; exits 1 when the sync fails or a receipt is refused.
  spool
      Print the spool directory.

  select [--mode replace|extend|baseline] [--policy success-count|recent-failure]
         [--allowance <duration>] [--main <ref>] [--json]
      Propose the landing tests for this checkout's change against <ref>
      (default origin/main) from the receipts in the spool. Prints each
      candidate as SELECTED, OVER-BUDGET, UNPRICED or UNAVAILABLE with its
      reason. replace (the default) fits ranked tests into the baseline's
      measured cost; extend keeps the baseline and adds ranked tests, within
      --allowance when given; both always select the tests the change touches.
      baseline prints the fixed list alone. The default policy is success-count.
  explain --test '<unit>' [--main <ref>] [--json]
      Show a unit's anchor and passes in each lane, its cost and its rerun command.
  pending [--main <ref>] [--json]
      List candidates that are unpriced on this machine or unavailable here.
  measure '<unit>'...
      Run units through scripts/landing-tests.sh to record their cost.
  replay --from <rev> [--main <ref>] [--machine <class>]
      For each main commit after <rev>, print as a JSON line what each policy
      would have selected from the receipts that existed when it was
      committed, and which failures observed on that commit it included.

The spool is $ATTN_TEST_HISTORY_DIR, or attn-test-history in the Git common directory.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "capture":
		os.Exit(capture(os.Args[2:]))
	case "recover":
		os.Exit(recoverWorking())
	case "summary":
		os.Exit(summary(os.Args[2:]))
	case "select":
		os.Exit(selectCommand(os.Args[2:]))
	case "explain":
		os.Exit(explainCommand(os.Args[2:]))
	case "pending":
		os.Exit(pendingCommand(os.Args[2:]))
	case "measure":
		os.Exit(measureCommand(os.Args[2:]))
	case "replay":
		os.Exit(replayCommand(os.Args[2:]))
	case "sync":
		os.Exit(syncHistory(os.Args[2:]))
	case "spool":
		dir, err := spoolDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "test-history:", err)
			os.Exit(1)
		}
		fmt.Println(dir)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func recoverWorking() int {
	dir, err := spoolDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "test-history:", err)
		return 1
	}
	recoveries, err := testhistory.Spool{Dir: dir}.Recover()
	if err != nil {
		fmt.Fprintln(os.Stderr, "test-history:", err)
		return 1
	}
	status := 0
	for _, recovery := range recoveries {
		if recovery.Err != nil {
			fmt.Fprintf(os.Stderr, "test-history: %s: %v\n", recovery.InvocationID, recovery.Err)
			status = 1
			continue
		}
		fmt.Printf("%s: %s\n", recovery.InvocationID, recovery.Outcome)
	}
	return status
}

func summary(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "test-history:", err)
		return 1
	}
	var receipt testhistory.Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		fmt.Fprintf(os.Stderr, "test-history: %s: %v\n", args[0], err)
		return 1
	}
	if receipt.Schema != testhistory.Schema {
		fmt.Fprintf(os.Stderr, "test-history: %s has schema %q; this reads %q\n", args[0], receipt.Schema, testhistory.Schema)
		return 1
	}
	status := 0
	for _, line := range testhistory.Summarize(receipt) {
		fmt.Println(line.Text)
		if !line.Settled() {
			status = 1
		}
	}
	return status
}
