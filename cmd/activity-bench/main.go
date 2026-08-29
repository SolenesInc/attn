package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "corpus":
		err = runCorpus(os.Args[2:])
	case "run":
		err = runMatrix(os.Args[2:])
	case "report":
		err = runReport(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "activity-bench: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `activity-bench — experiment loop for session activity lines

  corpus   sample windows from live sessions into the corpus (append + dedupe)
  run      run prompts x models x efforts over the corpus
  report   compare runs

Corpus entries are captured live, one pass per invocation, so rare states
(pending_approval, recoverable) accumulate by running it over time rather than
by fabricating them.

Data lives under `+"`--dir`"+` (default `+"`.activity-bench/`"+`, gitignored): real
windows carry source code and conversations and are not committed.
`)
}

// defaultDir holds real transcript excerpts, so it is gitignored on purpose.
const defaultDir = ".activity-bench"

func ensureDir(dir string) error {
	return os.MkdirAll(filepath.Clean(dir), 0o700)
}
