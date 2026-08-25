package main

import (
	"github.com/victorarias/attn/internal/lint/commentblock"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(commentblock.Analyzer)
}
