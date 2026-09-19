package main

import (
	"github.com/victorarias/attn/internal/lint/nocomments"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(nocomments.Analyzer)
}
