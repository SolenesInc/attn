package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/config"
)

func runDebugAttention(args []string) {
	fs := flag.NewFlagSet("debug attention", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tail := fs.Int("tail", defaultDebugTail, "only the last N lines (0 means no limit)")
	since := fs.String("since", "", "only entries within this Go duration of now (e.g. 10m, 1h)")
	session := fs.String("session", "", "only entries of sessions whose id starts with this")
	raw := fs.Bool("json", false, "print the raw lines")
	if err := parseFlags(fs, args); err != nil {
		fmt.Fprintf(os.Stderr, "debug attention: %v\n", err)
		exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "debug attention: unexpected arguments: %v\n", fs.Args())
		exit(2)
	}
	var cutoff time.Time
	if *since != "" {
		dur, err := time.ParseDuration(*since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "debug attention: invalid --since duration %q: %v\n", *since, err)
			exit(2)
		}
		cutoff = time.Now().Add(-dur)
	}
	lines, err := readLinesFile(filepath.Join(config.DataDir(), attention.DecisionLogFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	var shown []string
	for _, line := range lines {
		var entry attention.LogEntry
		if json.Unmarshal([]byte(line), &entry) != nil || entry.At.Before(cutoff) || !strings.HasPrefix(string(entry.Session), *session) {
			continue
		}
		if *raw {
			shown = append(shown, line)
			continue
		}
		shown = append(shown, describeAttentionEntry(entry))
	}
	for _, line := range tailLines(shown, *tail) {
		fmt.Println(line)
	}
}

func describeAttentionEntry(entry attention.LogEntry) string {
	at := entry.At.Local().Format(time.DateTime)
	switch entry.Kind {
	case attention.LogNotice:
		return fmt.Sprintf("%s %s %s notice to %s", at, entry.Session, entry.Checkpoint, entry.To)
	case attention.LogWaitEnded:
		return fmt.Sprintf("%s %s supervised wait ended", at, entry.Session)
	case attention.LogNoticeDeadline:
		return fmt.Sprintf("%s %s answer window of the notice to %s ran out", at, entry.Session, entry.To)
	case attention.LogFollowUp:
		verdictAt := ""
		if entry.VerdictAt != nil {
			verdictAt = entry.VerdictAt.Local().Format(time.DateTime)
		}
		return fmt.Sprintf("%s %s follow-up %s (verdict at %s)", at, entry.Session, entry.FollowUp, verdictAt)
	}
	verdict := string(entry.Verdict)
	if entry.Label != "" {
		verdict += fmt.Sprintf(" (%s)", entry.Label)
	}
	line := fmt.Sprintf("%s %s %s %s by %s: %s", at, entry.Session, entry.Checkpoint, verdict, entry.Decider, entry.Reason)
	if entry.LatencyMs > 0 {
		line += fmt.Sprintf(" (%d ms)", entry.LatencyMs)
	}
	return line
}
