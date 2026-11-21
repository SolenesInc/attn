package daemon

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) sessionCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"session instructions": command.Typed(d.runSessionInstructions),
		"session transcript":   command.Typed(d.runSessionTranscript),
		"session list":         command.Typed(d.runSessionList),
		"session show":         command.Typed(d.runSessionShow),
		"session priority":     command.Typed(d.runSessionPriority),
		"session snooze":       command.Typed(d.runSessionSnooze),
		"session unsnooze": command.Typed(func(ctx context.Context, c command.Caller, a command.SessionUnsnoozeArgs) (any, error) {
			return d.snoozeEach(c, a.Ids, nil), nil
		}),
		"session rename": command.Typed(d.runSessionRename),
		"session reopen": command.Typed(d.runSessionReopen),
	}
}

var sessionListPresets = map[string]int{"today": 0, "yesterday": 1, "7d": 6, "30d": 29}

func (d *Daemon) runSessionList(ctx context.Context, c command.Caller, a command.SessionListArgs) (any, error) {
	if a.Tree {
		set := []struct {
			name string
			on   bool
		}{{"closed", a.Closed}, {"all", a.All}, {"limit", a.Limit != nil}, {"before", a.Before != ""}, {"repository", a.Repository != ""}, {"last", a.Last != ""}, {"since", a.Since != ""}, {"until", a.Until != ""}, {"reopen", a.Reopen}}
		for _, arg := range set {
			if arg.on {
				return nil, command.Usage("{tree} supports only {profile}; omit {%s} to see complete live delegation chains", arg.name)
			}
		}
		return d.sessionTree(ctx, c, strings.TrimSpace(a.Profile))
	}
	if a.Closed && a.All {
		return nil, command.Usage("{closed} and {all} ask for different lists; pass one")
	}
	msg := &protocol.SessionListMessage{Cmd: protocol.CmdSessionList}
	if a.Closed {
		msg.Closed = protocol.Ptr(true)
	}
	if a.All {
		msg.All = protocol.Ptr(true)
	}
	if a.Limit != nil {
		if *a.Limit < 0 {
			return nil, command.Usage("{limit} %d is not a number of rows", *a.Limit)
		}
		if *a.Limit > 0 {
			msg.Limit = protocol.Ptr(int(*a.Limit))
		}
	}
	for field, value := range map[**string]string{&msg.Before: a.Before, &msg.ProfileID: a.Profile, &msg.Repository: a.Repository} {
		if value = strings.TrimSpace(value); value != "" {
			*field = protocol.Ptr(value)
		}
	}
	if a.Reopen {
		msg.Reopen = protocol.Ptr(true)
	}
	since, until, err := sessionListWindow(a, time.Now())
	if err != nil {
		return nil, err
	}
	if since != "" {
		msg.Since = protocol.Ptr(since)
	}
	if until != "" {
		msg.Until = protocol.Ptr(until)
	}
	result, err := d.sessionLedgerPage(msg, false)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func sessionListWindow(a command.SessionListArgs, now time.Time) (string, string, error) {
	if preset := strings.TrimSpace(a.Last); preset != "" {
		if strings.TrimSpace(a.Since) != "" || strings.TrimSpace(a.Until) != "" {
			return "", "", command.Usage("{last} already names a window; pass {since}/{until} instead, not both")
		}
		back, known := sessionListPresets[preset]
		if !known {
			names := make([]string, 0, len(sessionListPresets))
			for name := range sessionListPresets {
				names = append(names, name)
			}
			sort.Strings(names)
			return "", "", command.Usage("{last} %q is not a preset; pass one of: %s", preset, strings.Join(names, ", "))
		}
		since := localDayStart(now, back)
		if preset == "yesterday" {
			return since.Format(time.RFC3339), localDayStart(now, 0).Format(time.RFC3339), nil
		}
		return since.Format(time.RFC3339), "", nil
	}
	since, err := sessionListInstant("since", a.Since, now)
	if err != nil {
		return "", "", err
	}
	until, err := sessionListInstant("until", a.Until, now)
	return since, until, err
}

func localDayStart(now time.Time, daysBack int) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day()-daysBack, 12, 0, 0, 0, time.UTC)
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, now.Location())
	if start.Day() != day.Day() {
		_, dayBegins := start.ZoneBounds()
		return dayBegins
	}
	return start
}

func sessionListInstant(name, raw string, now time.Time) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if day, err := time.ParseInLocation("2006-01-02", raw, now.Location()); err == nil {
		return day.Format(time.RFC3339), nil
	}
	if at, err := time.Parse(time.RFC3339, raw); err == nil {
		return at.Format(time.RFC3339), nil
	}
	return "", command.Usage("{%s} %q is neither a date like 2026-09-05 nor an RFC3339 instant", name, raw)
}

func (d *Daemon) runSessionShow(ctx context.Context, c command.Caller, a command.SessionShowArgs) (any, error) {
	entry := d.store.SessionLedgerEntry(protocol.SessionID(strings.TrimSpace(a.ID)))
	if entry == nil {
		return nil, command.Refuse(command.CodeSessionNotFound, "no session %q in the ledger", strings.TrimSpace(a.ID))
	}
	result := &protocol.SessionShowResult{Entry: *entry}
	d.decorateLedgerEntryWithUsage(&result.Entry)
	d.decorateLedgerEntryHidden(&result.Entry)
	d.decorateLedgerEntryWithTitle(&result.Entry)
	verdict, err := d.resolveReopen(ctx, *entry)
	if err != nil {
		d.logf("session show: resolve reopen eligibility for session %s: %v", entry.ID, err)
	} else {
		result.Reopen = verdict.toProtocol()
	}
	return result, nil
}

func sessionTarget(c command.Caller, target string) (protocol.SessionID, error) {
	if id := protocol.SessionID(strings.TrimSpace(target)); id != "" {
		return id, nil
	}
	if c.Session != "" {
		return c.Session, nil
	}
	return "", command.Refuse(command.CodeNoSession, "no session: pass {target} or run inside an attn session")
}

func (d *Daemon) runSessionPriority(ctx context.Context, c command.Caller, a command.SessionPriorityArgs) (any, error) {
	if a.Priority != "on" && a.Priority != "off" {
		return nil, command.Usage("priority is on or off, not %q", a.Priority)
	}
	id, err := sessionTarget(c, a.Target)
	if err != nil {
		return nil, err
	}
	if err := d.setSessionPriority(&protocol.SetSessionPriorityMessage{SessionID: id, Priority: a.Priority == "on"}); err != nil {
		return nil, err
	}
	return fmt.Sprintf("%s priority %s", id, a.Priority), nil
}

func (d *Daemon) runSessionRename(ctx context.Context, c command.Caller, a command.SessionRenameArgs) (any, error) {
	id, err := sessionTarget(c, a.Target)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(a.Name)
	if err := d.renameSession(&protocol.RenameSessionMessage{SessionID: id, Label: name}); err != nil {
		return nil, err
	}
	return fmt.Sprintf("%s renamed to %s", id, strconv.Quote(name)), nil
}

func (d *Daemon) runSessionSnooze(ctx context.Context, c command.Caller, a command.SessionSnoozeArgs) (any, error) {
	if strings.TrimSpace(a.Until) == "" {
		return nil, command.Usage("{until} is required")
	}
	until, err := sessionSnoozeInstant(a.Until, time.Now())
	if err != nil {
		return nil, err
	}
	return d.snoozeEach(c, a.Ids, &until), nil
}

func (d *Daemon) snoozeEach(c command.Caller, ids []string, until *time.Time) protocol.SessionSnoozeResult {
	action := "unsnooze"
	if until != nil {
		action = "snooze"
	}
	result := protocol.SessionSnoozeResult{Outcomes: []protocol.SessionSnoozeOutcome{}}
	for _, raw := range ids {
		id := protocol.TrimID(protocol.SessionID(raw))
		outcome := protocol.SessionSnoozeOutcome{SessionID: id}
		err := d.authorizeSnooze(c.Session, id, action)
		if err == nil && until != nil {
			stamp := until.UTC().Format(time.RFC3339Nano)
			if err = d.handleSnoozeTurn(&protocol.SnoozeTurnMessage{SessionID: id, Until: stamp}); err == nil {
				outcome.Until = protocol.Ptr(stamp)
			}
		} else if err == nil {
			err = d.handleWakeTurn(&protocol.WakeTurnMessage{SessionID: id})
		}
		if err != nil {
			outcome.Error = protocol.Ptr(err.Error())
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result
}

func sessionSnoozeInstant(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if at, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		if !at.After(now) {
			return time.Time{}, command.Refuse(command.CodeBadUntil, "{until} %q is in the past; choose a future instant", raw)
		}
		return at, nil
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		if duration <= 0 {
			return time.Time{}, command.Refuse(command.CodeBadUntil, "{until} %q must be a positive duration", raw)
		}
		return now.Add(duration), nil
	}
	shape := command.Refuse(command.CodeBadUntil, "{until} %q must be an RFC3339 instant, a duration like 1h, or tomorrow, saturday or monday (optionally HH:MM)", raw)
	parts := strings.Fields(strings.ToLower(raw))
	if len(parts) == 0 || len(parts) > 2 {
		return time.Time{}, shape
	}
	hour, minute := 9, 0
	if len(parts) == 2 {
		clock, err := time.Parse("15:04", parts[1])
		if err != nil {
			return time.Time{}, command.Refuse(command.CodeBadUntil, "{until} %q: use HH:MM for the local time", raw)
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
		return time.Time{}, shape
	}
	at := time.Date(now.Year(), now.Month(), now.Day()+days, hour, minute, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 7)
	}
	return at, nil
}

func (d *Daemon) runSessionReopen(ctx context.Context, c command.Caller, a command.SessionReopenArgs) (any, error) {
	actions := []protocol.SessionReopenAction{protocol.SessionReopenActionReopen, protocol.SessionReopenActionRecreateWorktreeAndReopen, protocol.SessionReopenActionFetchRecreateAndReopen}
	action := protocol.SessionReopenAction(strings.TrimSpace(a.Action))
	if action != "" && !slices.Contains(actions, action) {
		names := make([]string, len(actions))
		for i, known := range actions {
			names[i] = string(known)
		}
		return nil, command.Refuse(command.CodeUnknownAction, "%q is not a reopen action; the actions are %s", string(action), strings.Join(names, ", "))
	}
	outcome, err := d.reopenSession(protocol.SessionID(strings.TrimSpace(a.ID)), action)
	if err != nil {
		return nil, err
	}
	return sessionReopenResult(outcome), nil
}
