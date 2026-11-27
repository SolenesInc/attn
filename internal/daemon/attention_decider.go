package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const attentionHeadlessTimeout = 30 * time.Second

const attentionVerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["OPEN","QUIET","FINISHED"]},"label":{"type":"string","enum":["unread report","asks you","nothing wakes it","stuck wait","finished"]},"reason":{"type":"string"}},"required":["verdict","reason"],"additionalProperties":false}`

func (d *Daemon) attentionSupervisor(agent protocol.SessionAgent) attention.Supervisor {
	return attention.Supervisor{Headless: func(ctx context.Context, f attention.Facts) (attention.Verdict, error) {
		return d.attentionHeadless(ctx, agent, f)
	}}
}

func (d *Daemon) attentionHeadless(ctx context.Context, agent protocol.SessionAgent, f attention.Facts) (attention.Verdict, error) {
	if d.headlessTaskRefused("attention supervisor") {
		return attention.Verdict{Failure: attention.FailureHeadlessOff}, fmt.Errorf("headless tasks off")
	}
	failed := func(err error) (attention.Verdict, error) {
		return attention.Verdict{Failure: attention.FailureHarness}, err
	}
	id, _ := d.classifierHarness(agent)
	oneShot, err := d.oneShot(id)
	if err != nil {
		return failed(err)
	}
	workDir, err := headlessScratchCwd()
	if err != nil {
		return failed(err)
	}
	facts, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return failed(err)
	}
	ctx, cancel := context.WithTimeout(ctx, attentionHeadlessTimeout)
	defer cancel()
	choice := d.classifierChoice(ctx, id)
	result, err := oneShot.Run(ctx, harness.OneShotRequest{
		Model:  choice.Model,
		Effort: choice.Effort,
		Input:  prompts.RenderText("attention", "supervisor", prompts.Values{"facts": string(facts)}),
		Dir:    workDir,
		Bound:  harness.Bound{MaxTurns: classifier.MaxTurns},
		Schema: json.RawMessage(attentionVerdictSchema),
	})
	if err != nil {
		return failed(err)
	}
	model := choice.Model
	if model == "" {
		model = id
	}
	verdict, ok := parseAttentionVerdict(result.Structured, result.Text)
	if !ok {
		return attention.Verdict{Failure: attention.FailureUnusableAnswer}, fmt.Errorf("unusable answer from headless/%s", model)
	}
	verdict.Decider = "headless/" + model
	return verdict, nil
}

func parseAttentionVerdict(structured json.RawMessage, text string) (attention.Verdict, bool) {
	answer := []byte(structured)
	if len(answer) == 0 {
		answer = []byte(strings.TrimSpace(text))
	}
	var parsed struct {
		Verdict string          `json:"verdict"`
		Label   attention.Label `json:"label"`
		Reason  string          `json:"reason"`
	}
	if err := json.Unmarshal(answer, &parsed); err != nil {
		return attention.Verdict{}, false
	}
	kinds := map[string]attention.Kind{"OPEN": attention.Open, "QUIET": attention.Quiet, "FINISHED": attention.Finished}
	kind, ok := kinds[parsed.Verdict]
	verdict := attention.Verdict{Kind: kind, Reason: strings.TrimSpace(parsed.Reason)}
	if kind != attention.Quiet && slices.Contains(attention.Labels, parsed.Label) {
		verdict.Label = parsed.Label
	}
	return verdict, ok
}
