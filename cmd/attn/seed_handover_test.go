package main

import "testing"

func TestSeedHandoverWorkerFlagsParseAroundTheSeedID(t *testing.T) {
	f := newSeedFlags("handover")
	positionals := f.parse("handover", []string{
		"--agent", "claude", "s-7k3f9m", "-m", "Continue from the parser.",
		"--model", "sonnet", "--effort", "high", "--cwd", "/tmp/work",
		"--name", "parser", "--request-id", "handover-1", "--yolo",
	})
	if len(positionals) != 1 || positionals[0] != "s-7k3f9m" {
		t.Fatalf("positionals = %v", positionals)
	}
	if *f.agent != "claude" || *f.model != "sonnet" || *f.effort != "high" ||
		*f.cwd != "/tmp/work" || *f.name != "parser" || *f.requestID != "handover-1" ||
		!*f.yolo || *f.message != "Continue from the parser." {
		t.Fatalf("Handover flags parsed incorrectly")
	}
}
