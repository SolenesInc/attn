package daemon

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

const (
	seedReminderKind        = "seed_reminder"
	seedReminderTick        = time.Minute
	seedReminderTickTimeout = 2 * time.Minute
	seedReminderSource      = "seed-reminder"
)

func (d *Daemon) registerSeedReminderCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(
		seedReminderKind,
		seedReminderTick,
		d.seedReminderHandler,
		jobs.HandlerConfig{Timeout: seedReminderTickTimeout},
	); err != nil {
		d.logf("seed reminders: register tick: %v", err)
	}
}

func (d *Daemon) handleSeedReminder(conn net.Conn, msg *protocol.SeedReminderMessage, setter who.Actor) {
	result, err := d.seedReminderRequest(msg, setter)
	if err != nil {
		d.sendGardenError(conn, "remind", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedReminderResult: result})
}

func (d *Daemon) seedReminderRequest(msg *protocol.SeedReminderMessage, setter who.Actor) (*protocol.SeedReminderResult, error) {
	seedID := strings.TrimSpace(msg.SeedID)
	if protocol.Deref(msg.Clear) {
		if msg.At != nil || msg.In != nil || msg.Message != nil {
			return nil, protocol.Refuse(protocol.RefusalInvalidInput, "--clear takes no time or message: attn seed remind %s --clear", seedID)
		}
		return d.writeSeedReminder(seedID, nil, setter)
	}
	now := d.clock.Now()
	at, err := reminderTime(seedID, protocol.Deref(msg.At), protocol.Deref(msg.In), now)
	if err != nil {
		return nil, err
	}
	reminder := garden.Reminder{
		At: at, Message: strings.TrimSpace(protocol.Deref(msg.Message)),
		SetAt: now.UTC(), SetBy: setter,
	}
	if err := garden.ValidateReminder(reminder, seedID); err != nil {
		return nil, err
	}
	return d.writeSeedReminder(seedID, &reminder, setter)
}

func reminderTime(seedID, at, in string, now time.Time) (time.Time, error) {
	at, in = strings.TrimSpace(at), strings.TrimSpace(in)
	var when time.Time
	switch {
	case at != "" && in != "":
		return time.Time{}, protocol.Refuse(protocol.RefusalInvalidInput, "pass --in or --at, not both")
	case in != "":
		d, err := time.ParseDuration(in)
		if err != nil || d <= 0 {
			return time.Time{}, protocol.Refuse(protocol.RefusalInvalidInput, "--in takes a positive duration such as 20m or 2h30m, got %q", in)
		}
		when = now.Add(d)
	case at != "":
		parsed, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return time.Time{}, protocol.Refuse(protocol.RefusalInvalidInput, "--at takes an RFC3339 time such as 2026-11-04T14:20:00Z, got %q", at)
		}
		if !parsed.After(now) {
			return time.Time{}, protocol.Refuse(protocol.RefusalInvalidInput, "--at %s has already passed", at)
		}
		when = parsed
	default:
		return time.Time{}, protocol.Refuse(protocol.RefusalInvalidInput, "a reminder needs a time: attn seed remind %s --in 20m -m \"…\"", seedID)
	}
	return when.UTC().Truncate(time.Second), nil
}

func (d *Daemon) writeSeedReminder(seedID string, reminder *garden.Reminder, setter who.Actor) (*protocol.SeedReminderResult, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return nil, err
		}
		armed, err := d.store.SeedReminder(seed.ID)
		if err != nil {
			return nil, err
		}
		if reminder == nil && armed == nil {
			return &protocol.SeedReminderResult{Seed: d.seedWire(seed, doc)}, nil
		}
		if reminder != nil && garden.Closed(seed.Status) {
			return nil, protocol.Refuse(protocol.RefusalWrongState,
				"%s is %s and takes no reminder; replant it first: attn seed replant %s", seed.ID, seed.Status, seed.ID)
		}
		if err := d.checkSeedStanding(seed, setter, "set its reminder"); err != nil {
			return nil, err
		}
		body := "Cleared the reminder."
		if reminder != nil {
			body = fmt.Sprintf("Reminder set for %s: %s", reminder.At.Format(time.RFC3339), reminder.Message)
		}
		cause, _ := d.sessionOfActor(setter)
		entry := seedMoveNote{
			Note:              garden.Note{Seed: seed.ID, Kind: garden.NoteKindNote, Body: body, Author: setter},
			CausedBySessionID: cause,
		}
		d.lockGardenRoles()
		written, _, err := d.writeSeedChange(*schema, seed, doc.Rev, seedChange{author: setter, cause: cause, entries: []seedMoveNote{entry}, arm: reminder, disarm: reminder == nil})
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &protocol.SeedReminderResult{Seed: d.seedWire(seed, written), Changed: true}, nil
	}
	return nil, protocol.Refuse(protocol.RefusalConflict,
		"%s was rewritten under all %d attempts to save its reminder; read it again with `attn seed show %s`", seedID, attempts, seedID)
}

func (d *Daemon) seedReminderHandler(context.Context, *jobs.Job) (any, error) {
	d.fireDueSeedReminders(d.clock.Now())
	return nil, nil
}

func (d *Daemon) fireDueSeedReminders(now time.Time) {
	if d.isRecovering() {
		return
	}
	release, held := d.life.Hold("fireDueSeedReminders")
	if !held {
		return
	}
	defer release()
	due, err := d.store.DueSeedReminders(now)
	if err != nil {
		d.logf("seed reminders: list due: %v", err)
		return
	}
	for _, seedID := range due {
		pausepoint.At(pausepoint.SeedReminderDue)
		fired, err := d.fireSeedReminder(seedID, now)
		if err != nil {
			d.logf("seed reminders: %s: %v", seedID, err)
			continue
		}
		if fired {
			d.kickInboxAfterCommit(who.ToTenderOf(seedID))
		}
	}
}

func (d *Daemon) fireSeedReminder(seedID string, now time.Time) (bool, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return false, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if refusal := protocol.RefusalOf(err); refusal != nil && *refusal == protocol.RefusalNotFound {
			return false, d.store.DisarmSeedReminder(seedID)
		}
		if err != nil {
			return false, err
		}
		reminder, err := d.store.SeedReminder(seedID)
		if err != nil {
			return false, err
		}
		pausepoint.At(pausepoint.SeedReminderRead)
		if reminder == nil || reminder.At.After(now) {
			return false, nil
		}
		if garden.Closed(seed.Status) {
			disarmed, err := d.store.DisarmSeedReminderAtRev(*schema, seedID, doc.Rev)
			if err != nil || disarmed {
				return false, err
			}
			continue
		}
		item := seedReminderItem(seed, *reminder, d.actorView(reminder.SetBy).Name)
		d.lockGardenRoles()
		_, _, err = d.writeSeedChange(*schema, seed, doc.Rev, seedChange{author: who.Attn(), typed: &item, disarm: true})
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		return err == nil, err
	}
	return false, fmt.Errorf("rewritten under all %d attempts to fire its reminder", attempts)
}

func seedReminderItem(seed garden.Seed, reminder garden.Reminder, setBy string) inbox.Item {
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
		seedReminderSource, seed.ID, reminder.SetAt.Format(time.RFC3339Nano), reminder.At.Format(time.RFC3339),
	}, "\x00"))).String()
	text := fmt.Sprintf("Reminder for seed %s %q", seed.ID, seed.Title)
	if setBy != "" {
		text += ", set by " + setBy
	}
	text += ":\n" + reminder.Message
	return inbox.Item{
		ID: id, To: who.ToTenderOf(seed.ID), Kind: inbox.Notice,
		Source: seedReminderSource, Hint: "reminder", Text: text,
	}
}
