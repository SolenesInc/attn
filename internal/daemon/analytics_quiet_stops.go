package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

const (
	// 99% of 4,357 assistant messages across the 300 newest real transcripts fit in 3,575 characters.
	quietStopMessageMaxChars = 4000
	quietStopVerdictTimeout  = 30 * time.Second
	quietStopNoMessage       = "no_message"
	quietStopFailed          = "failed"
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
	message, err := m.RunLastMessage(run, quietStopMessageMaxChars)
	switch {
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
	driver, _ := classifierDriver(agent)
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return "", errors.New(driver.Name() + " runs no headless tasks")
	}
	executable, err := d.machineAgentExecutable(driver, "")
	if err != nil {
		return "", err
	}
	workDir, err := headlessScratchCwd()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), quietStopVerdictTimeout)
	defer cancel()
	request := agentdriver.HeadlessTaskRequest{
		Executable:   executable,
		Model:        d.classifierModel(ctx, driver.Name()),
		Prompt:       classifier.BuildQuietStopPrompt(message),
		WorkDir:      workDir,
		DisableTools: true,
		MaxTurns:     classifier.ClaudeMaxTurns,
		OutputSchema: json.RawMessage(classifier.QuietStopVerdictSchema),
	}
	if driver.Name() == "codex" {
		request.ReasoningEffort = classifier.CodexClassifierReasoningEffort()
	}
	result, err := provider.RunHeadlessTask(ctx, request)
	if err != nil {
		return "", err
	}
	verdict, _ := classifier.ParseQuietStopVerdict(result.StructuredOutput, result.Text)
	return verdict, nil
}
