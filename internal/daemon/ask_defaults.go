package daemon

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	askDefaultKind        = "ask_default_due"
	askDefaultTick        = time.Minute
	askDefaultTickTimeout = 2 * time.Minute
	askDefaultSource      = "ask-default"
)

func (d *Daemon) registerAskDefaultCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(askDefaultKind, askDefaultTick, d.askDefaultHandler, jobs.HandlerConfig{Timeout: askDefaultTickTimeout}); err != nil {
		d.logf("asks: register default tick: %v", err)
	}
}

func (d *Daemon) askDefaultHandler(context.Context, *jobs.Job) (any, error) {
	d.fireDueAskDefaults(d.clock.Now())
	return nil, nil
}

func (d *Daemon) fireDueAskDefaults(now time.Time) {
	if d.isRecovering() {
		return
	}
	release, held := d.life.Hold("fireDueAskDefaults")
	if !held {
		return
	}
	defer release()
	due, err := d.store.AsksWithDefaultDue(now)
	if err != nil {
		d.logf("asks: list due defaults: %v", err)
		return
	}
	for _, stored := range due {
		shown, err := d.readAsk(stored)
		if err != nil {
			d.logf("asks: %s: %v", stored.ID, err)
			continue
		}
		notice, err := askDefaultNotice(shown)
		if err != nil {
			d.logf("asks: %s: %v", stored.ID, err)
			continue
		}
		fired, err := d.store.FireAskDefault(stored.ID, notice, now)
		if err != nil {
			d.logf("asks: firing the default of %s: %v", stored.ID, err)
			continue
		}
		if fired {
			d.kickInboxAfterCommit(stored.To)
		}
	}
}

func askDefaultNotice(shown shownAsk) (inbox.Item, error) {
	a := shown.wire
	question, ok := defaultedQuestion(a)
	if !ok {
		return inbox.Item{}, fmt.Errorf("its markdown no longer names a default")
	}
	text := prompts.RenderText("ask", "default-due", prompts.Values{
		"subject": strconv.Quote(a.Subject),
		"ask_id":  a.ID,
		"prompt":  question.Prompt,
		"choice":  question.Default.Choice,
	})
	return inbox.Item{
		ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(askDefaultSource+"\x00"+a.ID)).String(),
		To: shown.stored.To, Kind: inbox.Notice, Source: askDefaultSource, Hint: reminderHint, Text: text,
	}, nil
}

func defaultDueAt(a protocol.Ask) time.Time {
	question, ok := defaultedQuestion(a)
	if !ok {
		return time.Time{}
	}
	dueAt, _ := time.Parse(time.RFC3339, question.Default.DueAt)
	return dueAt
}

func defaultedQuestion(a protocol.Ask) (protocol.AskQuestion, bool) {
	for _, question := range ask.Questions(a.Parts) {
		if question.Default != nil {
			return question, true
		}
	}
	return protocol.AskQuestion{}, false
}
