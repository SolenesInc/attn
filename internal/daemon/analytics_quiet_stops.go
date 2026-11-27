package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

const (
	quietStopVerdictTimeout = 30 * time.Second
	quietStopNoMessage      = "no_message"
	quietStopFailed         = "failed"
)

type quietStopProps struct {
	Verdict string `json:"verdict"`
	Failure string `json:"failure,omitempty"`
}

func (d *Daemon) recordQuietStopVerdict(m *sessionmonitor.Monitor, session protocol.SessionID, run sessionmonitor.Run) {
	props := d.judgeQuietStop(m, run)
	if !d.analyticsCapturedSince(run.StartedAt) {
		return
	}
	if err := d.appendAnalyticsAt(analytics.StreamRun, "quiet_stop", string(session), run.EndedAt, props); err != nil {
		d.logf("analytics: %v", err)
	}
}

func (d *Daemon) judgeQuietStop(m *sessionmonitor.Monitor, run sessionmonitor.Run) quietStopProps {
	if d.headlessTaskRefused("quiet stop verdict") {
		return quietStopProps{Verdict: quietStopFailed, Failure: "headless_tasks_off"}
	}
	message, err := m.RunLastMessage(run, attention.LastMessageMaxChars)
	switch {
	case errors.Is(err, sessionmonitor.ErrRunMessageUnavailable):
		return quietStopProps{Verdict: quietStopFailed, Failure: "message_unavailable"}
	case err != nil:
		d.logf("quiet stop verdict: session=%s transcript: %v", m.Session(), err)
		return quietStopProps{Verdict: quietStopFailed, Failure: "transcript"}
	case strings.TrimSpace(message) == "":
		return quietStopProps{Verdict: quietStopNoMessage}
	}
	verdict, err := d.askQuietStopVerdict(m.Agent(), message)
	switch {
	case err != nil:
		d.logf("quiet stop verdict: session=%s harness: %v", m.Session(), err)
		return quietStopProps{Verdict: quietStopFailed, Failure: "harness"}
	case verdict == "":
		return quietStopProps{Verdict: quietStopFailed, Failure: "unusable_answer"}
	}
	return quietStopProps{Verdict: verdict}
}

func (d *Daemon) askQuietStopVerdict(agent protocol.SessionAgent, message string) (string, error) {
	id, _ := d.classifierHarness(agent)
	oneShot, err := d.oneShot(id)
	if err != nil {
		return "", err
	}
	workDir, err := headlessScratchCwd()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), quietStopVerdictTimeout)
	defer cancel()
	choice := d.classifierChoice(ctx, id)
	result, err := oneShot.Run(ctx, harness.OneShotRequest{
		Model:  choice.Model,
		Effort: choice.Effort,
		Input:  classifier.BuildQuietStopPrompt(message),
		Dir:    workDir,
		Bound:  harness.Bound{MaxTurns: classifier.MaxTurns},
		Schema: json.RawMessage(classifier.QuietStopVerdictSchema),
	})
	if err != nil {
		return "", err
	}
	verdict, _ := classifier.ParseQuietStopVerdict(result.Structured, result.Text)
	return verdict, nil
}
