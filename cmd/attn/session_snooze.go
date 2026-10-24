package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

type sessionSnoozeArgs struct {
	ids   []protocol.SessionID
	until time.Time
}

func sessionSnoozeInstant(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if at, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		if !at.After(now) {
			return time.Time{}, fmt.Errorf("--until %q is in the past; choose a future instant", raw)
		}
		return at, nil
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		if duration <= 0 {
			return time.Time{}, fmt.Errorf("--until %q must be a positive duration", raw)
		}
		return now.Add(duration), nil
	}
	parts := strings.Fields(strings.ToLower(raw))
	if len(parts) == 0 || len(parts) > 2 {
		return time.Time{}, fmt.Errorf("--until %q must be an RFC3339 instant, a duration like 1h, or tomorrow, saturday or monday (optionally HH:MM)", raw)
	}
	hour, minute := 9, 0
	if len(parts) == 2 {
		clock, err := time.Parse("15:04", parts[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("--until %q: use HH:MM for the local time", raw)
		}
		hour, minute = clock.Hour(), clock.Minute()
	}
	days := 1
	switch parts[0] {
	case "tomorrow":
	case "saturday", "monday":
		weekday := time.Monday
		if parts[0] == "saturday" {
			weekday = time.Saturday
		}
		days = (int(weekday) - int(now.Weekday()) + 7) % 7
	default:
		return time.Time{}, fmt.Errorf("--until %q must be an RFC3339 instant, a duration like 1h, or tomorrow, saturday or monday (optionally HH:MM)", raw)
	}
	at := time.Date(now.Year(), now.Month(), now.Day()+days, hour, minute, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 7)
	}
	return at, nil
}

func parseSessionSnoozeArgs(args []string, snooze bool, now time.Time) (sessionSnoozeArgs, error) {
	fs := flag.NewFlagSet("session snooze", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var until string
	if snooze {
		fs.StringVar(&until, "until", "", "when to unsnooze")
	}
	var parsed sessionSnoozeArgs
	for rest := args; ; {
		if err := parseFlags(fs, rest); err != nil {
			return parsed, err
		}
		if fs.NArg() == 0 {
			break
		}
		id := protocol.TrimID(protocol.SessionID(fs.Arg(0)))
		if id == "" {
			return parsed, errors.New("session id cannot be empty")
		}
		parsed.ids = append(parsed.ids, id)
		rest = fs.Args()[1:]
	}
	if len(parsed.ids) == 0 {
		return parsed, errors.New("at least one session id is required")
	}
	if snooze {
		if strings.TrimSpace(until) == "" {
			return parsed, errors.New("--until is required")
		}
		var err error
		parsed.until, err = sessionSnoozeInstant(until, now)
		if err != nil {
			return parsed, err
		}
	}
	return parsed, nil
}

func runSessionSnooze(args []string, snooze bool) {
	action := "unsnooze"
	if snooze {
		action = "snooze"
	}
	parsed, err := parseSessionSnoozeArgs(args, snooze, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "session %s: %v\n", action, err)
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}
	caller := currentSessionOrExit()
	cli := client.New("")
	failed := false
	for _, id := range parsed.ids {
		if snooze {
			err = cli.SnoozeSession(caller, id, parsed.until)
		} else {
			err = cli.UnsnoozeSession(caller, id)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "session %s %s: %v\n", action, id, err)
			failed = true
			continue
		}
		if snooze {
			fmt.Printf("%s snoozed until %s\n", id, parsed.until.UTC().Format(time.RFC3339Nano))
		} else {
			fmt.Printf("%s unsnoozed\n", id)
		}
	}
	if failed {
		os.Exit(1)
	}
}
