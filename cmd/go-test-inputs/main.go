package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/devdiff"
	"github.com/victorarias/attn/internal/devdiff/gotestinputs"
)

func main() {
	base := flag.String("base", "origin/next", "ref the change will merge into")
	flag.Parse()
	change, err := devdiff.Since(*base)
	var paths []string
	if err == nil {
		paths, err = change.Paths()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-test-inputs:", err)
		os.Exit(1)
	}
	inputs := gotestinputs.Among(paths)
	if len(inputs) == 0 {
		fmt.Fprintf(os.Stderr, "nothing the Go suite reads changed since %s (%d changed paths)\n", *base, len(paths))
		fmt.Println(gotestinputs.Unchanged)
		return
	}
	fmt.Fprintf(os.Stderr, "%d of %d changed paths since %s are Go suite inputs, such as %s\n", len(inputs), len(paths), *base, inputs[0])
	fmt.Println(gotestinputs.Changed)
}
