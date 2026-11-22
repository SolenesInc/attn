package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
)

const ModelCallLimitTimer stepclock.Name = "model-call-limit"

const (
	modelCallLogPruneKind     = "model_call_log_prune"
	modelCallLogPruneInterval = time.Hour
	modelCallLogPruneTimeout  = time.Minute
	modelCallLogRetention     = 30 * 24 * time.Hour
)

// Each running call holds a harness process of 200 to 300 MB; healthy use is
// one dictation at a time, and six ran in parallel in the probes (plot
// s-jhntry, D6).
const modelCallsRunningTripwire = 8

type modelCallTarget struct {
	harness string
	oneShot harness.OneShot
	model   string
}

type modelChoice struct {
	tier  modeltiers.Tier
	model string
}

func parseModelChoice(raw string) modelChoice {
	value := strings.TrimSpace(raw)
	switch tier := modeltiers.Tier(strings.ToLower(value)); tier {
	case "":
		return modelChoice{tier: modeltiers.Standard}
	case modeltiers.Light, modeltiers.Standard, modeltiers.Deep:
		return modelChoice{tier: tier}
	}
	return modelChoice{model: value}
}

func parseModelCallTier(raw string) (modeltiers.Tier, error) {
	switch tier := modeltiers.Tier(raw); tier {
	case modeltiers.Light, modeltiers.Standard, modeltiers.Deep:
		return tier, nil
	}
	return "", fmt.Errorf("tier must be light, standard or deep, not %q", raw)
}

func (d *Daemon) modelCallHarnesses() []string {
	var names []string
	for _, h := range d.harnesses() {
		if _, ok := harness.As[harness.OneShot](h); ok && h.Health(d.life.Context()).Offered {
			names = append(names, h.Facts().ID)
		}
	}
	slices.Sort(names)
	return names
}

func (d *Daemon) validateModelCallsAgent(value string) error {
	name := strings.ToLower(strings.TrimSpace(value))
	if name == "" || slices.Contains(d.modelCallHarnesses(), name) {
		return nil
	}
	return fmt.Errorf("model calls are answered by %s, not %q", strings.Join(d.modelCallHarnesses(), " or "), value)
}

func (d *Daemon) newSessionAgent() string {
	return harnessID(d.setting(setting.NewSessionAgent))
}

func (d *Daemon) modelCallDefaultHarness() string {
	if name := strings.ToLower(strings.TrimSpace(d.setting(setting.ModelCallsAgent))); name != "" {
		return name
	}
	return d.newSessionAgent()
}

func (d *Daemon) modelCallHarness(named string) (string, harness.OneShot, error) {
	name := strings.ToLower(strings.TrimSpace(named))
	if name == "" {
		name = d.modelCallDefaultHarness()
	}
	answering := d.modelCallHarnesses()
	if !slices.Contains(answering, name) {
		return "", nil, d.modelCallHarnessRefusal(named, name, answering)
	}
	oneShot, err := d.oneShot(name)
	if err != nil {
		return "", nil, err
	}
	return name, oneShot, nil
}

func (d *Daemon) modelCallHarnessRefusal(named, name string, answers []string) error {
	answering := strings.Join(answers, " or ")
	if strings.TrimSpace(named) == "" && strings.TrimSpace(d.setting(setting.ModelCallsAgent)) == "" {
		if name == "" {
			return fmt.Errorf("model calls name no harness; set model_calls.agent to %s", answering)
		}
		return fmt.Errorf("model calls default to the new-session agent %q, which does not answer model calls; set model_calls.agent to %s", name, answering)
	}
	return fmt.Errorf("%q does not answer model calls; model calls are answered by %s", name, answering)
}

func (d *Daemon) modelCallTarget(ctx context.Context, msg *protocol.SenderModelCallMessage) (modelCallTarget, error) {
	if msg.Tier != nil && strings.TrimSpace(protocol.Deref(msg.Model)) != "" {
		return modelCallTarget{}, errors.New("name a tier or a model, not both")
	}
	choice := parseModelChoice(d.setting(setting.ModelCallsModel))
	switch {
	case msg.Tier != nil:
		tier, err := parseModelCallTier(string(*msg.Tier))
		if err != nil {
			return modelCallTarget{}, err
		}
		choice = modelChoice{tier: tier}
	case strings.TrimSpace(protocol.Deref(msg.Model)) != "":
		choice = modelChoice{model: strings.TrimSpace(*msg.Model)}
	}
	name, oneShot, err := d.modelCallHarness(protocol.Deref(msg.Harness))
	if err != nil {
		return modelCallTarget{}, err
	}
	if _, err := d.machineAgentExecutable(name, ""); err != nil {
		return modelCallTarget{}, fmt.Errorf("%s executable was not found: %w", name, err)
	}
	model := choice.model
	if model == "" {
		model = d.resolveTierModel(ctx, name, choice.tier, "", d.factsOrNone(name).Tiers[choice.tier].Model)
	}
	return modelCallTarget{harness: name, oneShot: oneShot, model: model}, nil
}

type modelCallsRunning struct {
	mu      sync.Mutex
	running int
}

func (r *modelCallsRunning) admit() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running >= modelCallsRunningTripwire {
		return false
	}
	r.running++
	return true
}

func (r *modelCallsRunning) done() {
	r.mu.Lock()
	r.running--
	r.mu.Unlock()
}

type modelCallOutcome struct {
	target  modelCallTarget
	refused error
	result  harness.OneShotResult
	err     error
}

func (d *Daemon) handleSenderModelCall(conn net.Conn, msg *protocol.SenderModelCallMessage) {
	arrived := d.clock.Now()
	sender, err := d.requirePairedSender(msg.SenderToken)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if !headless.Enabled() {
		d.sendError(conn, headless.Refusal("model call").Error())
		return
	}
	if msg.LimitMs <= 0 {
		d.sendError(conn, fmt.Sprintf("limit_ms must be a positive number of milliseconds, not %d", msg.LimitMs))
		return
	}
	if strings.TrimSpace(msg.Input) == "" {
		d.sendError(conn, "input must not be empty; send the text the model answers")
		return
	}
	if !d.modelCallsRunning.admit() {
		d.sendError(conn, fmt.Sprintf("%d model calls are already running, attn's limit; retry when one answers", modelCallsRunningTripwire))
		return
	}
	defer d.modelCallsRunning.done()
	defer d.clock.Hold()()
	limit := time.Duration(msg.LimitMs) * time.Millisecond
	deadline := d.clock.NewTimer(ModelCallLimitTimer, limit)
	defer deadline.Stop()
	ctx, cancel := context.WithCancel(d.life.Context())
	defer cancel()
	goTransport(func() {
		_, _ = io.Copy(io.Discard, conn)
		cancel()
	})

	answered := make(chan modelCallOutcome, 1)
	var target atomic.Pointer[modelCallTarget]
	var bySpare atomic.Bool
	if !d.life.Go("modelCallAnswer", func() { answered <- d.answerModelCall(ctx, sender.ProfileID, msg, &target, &bySpare) }) {
		return
	}
	var outcome modelCallOutcome
	passedLimit := false
	select {
	case outcome = <-answered:
	case <-deadline.C():
		cancel()
		passedLimit = true
		outcome.err = fmt.Errorf("the model did not answer within %s", limit)
	case <-ctx.Done():
		outcome.err = errors.New("the caller closed the connection")
	}
	if outcome.refused != nil {
		d.sendError(conn, outcome.refused.Error())
		return
	}
	if resolved := target.Load(); resolved != nil {
		outcome.target = *resolved
	}
	took := d.clock.Now().Sub(arrived)
	d.logModelCall(sender.Name, msg.Purpose, outcome.target, bySpare.Load(), took, outcome)
	ended := protocol.ModelCallOutcomeFailed
	response := protocol.SenderModelCallResult{Harness: outcome.target.harness, Model: outcome.target.model}
	switch {
	case outcome.err == nil:
		ended = protocol.ModelCallOutcomeAnswered
		response.Text = protocol.Ptr(outcome.result.Text)
		if outcome.result.Usage.ModelTime > 0 {
			response.ModelMs = protocol.Ptr(int(outcome.result.Usage.ModelTime.Milliseconds()))
		}
	case passedLimit:
		ended = protocol.ModelCallOutcomeLimit
		response.Error = protocol.Ptr(outcome.err.Error())
		response.LimitMs = protocol.Ptr(msg.LimitMs)
	case ctx.Err() != nil:
		ended = protocol.ModelCallOutcomeStopped
	default:
		response.Error = protocol.Ptr(outcome.err.Error())
	}
	d.recordModelCall(sender, msg.Purpose, outcome, arrived, took, ended)
	if ended == protocol.ModelCallOutcomeStopped {
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SenderModelCallResult: &response})
}

func (d *Daemon) answerModelCall(ctx context.Context, profile string, msg *protocol.SenderModelCallMessage, resolved *atomic.Pointer[modelCallTarget], bySpare *atomic.Bool) modelCallOutcome {
	d.modelCallSpare.used(d)
	target, err := d.modelCallTarget(ctx, msg)
	if err != nil {
		return modelCallOutcome{refused: err}
	}
	resolved.Store(&target)
	request := harness.OneShotRequest{Profile: profile, Model: target.model, System: msg.System, Input: msg.Input}
	if spare := d.modelCallSpare.take(d, target.harness); spare != nil {
		bySpare.Store(true)
		result, err := spare.Answer(ctx, request)
		if !errors.Is(err, harness.ErrSpareUnusable) {
			result, err = modelCallAnswer(result, err)
			return answeredBy(target, resolved, result, err)
		}
		bySpare.Store(false)
		d.logf("model call: the %s spare could not answer, so the call runs cold: %v", target.harness, err)
	}
	result, err := modelCallAnswer(target.oneShot.Run(ctx, request))
	if err != nil {
		err = fmt.Errorf("%s: %w", target.harness, err)
	}
	return answeredBy(target, resolved, result, err)
}

func answeredBy(target modelCallTarget, resolved *atomic.Pointer[modelCallTarget], result harness.OneShotResult, err error) modelCallOutcome {
	if target.model == "" && result.Model != "" {
		target.model = result.Model
		resolved.Store(&target)
	}
	return modelCallOutcome{target: target, result: result, err: err}
}

func modelCallAnswer(result harness.OneShotResult, err error) (harness.OneShotResult, error) {
	if err != nil {
		if result.Diagnostics != "" {
			return harness.OneShotResult{}, errors.New(result.Diagnostics)
		}
		return harness.OneShotResult{}, err
	}
	if result.Text = strings.TrimSpace(result.Text); result.Text == "" {
		return harness.OneShotResult{}, errors.New("the model returned no text")
	}
	return result, nil
}

func (d *Daemon) logModelCall(sender, purpose string, target modelCallTarget, bySpare bool, took time.Duration, outcome modelCallOutcome) {
	result := "answered"
	if outcome.err != nil {
		result = "failed: " + outcome.err.Error()
	}
	d.logf("model call: sender=%q purpose=%q harness=%s model=%s spare=%t took=%s model_ms=%d tokens_in=%d tokens_out=%d cost_usd=%.4f outcome=%s",
		sender, purpose, target.harness, target.model, bySpare, took.Round(time.Millisecond), outcome.result.Usage.ModelTime.Milliseconds(),
		outcome.result.Usage.In, outcome.result.Usage.Out, outcome.result.Usage.CostUSD, result)
}

func (d *Daemon) recordModelCall(sender *store.PairedSender, purpose string, outcome modelCallOutcome, arrived time.Time, took time.Duration, ended protocol.ModelCallOutcome) {
	call := protocol.ModelCall{
		At:        store.ModelCallTime(arrived),
		Sender:    sender.Name,
		Purpose:   purpose,
		Harness:   outcome.target.harness,
		Model:     outcome.target.model,
		TookMs:    int(took.Milliseconds()),
		TokensIn:  outcome.result.Usage.In,
		TokensOut: outcome.result.Usage.Out,
		CostUsd:   outcome.result.Usage.CostUSD,
		Outcome:   ended,
	}
	if outcome.result.Usage.ModelTime > 0 {
		call.ModelMs = protocol.Ptr(int(outcome.result.Usage.ModelTime.Milliseconds()))
	}
	if err := d.store.AddModelCall(sender.ProfileID, call); err != nil {
		d.logf("model call: record the call in the model call log: %v", err)
	}
}

func (d *Daemon) handleModelCallLog(conn net.Conn, msg *protocol.ModelCallLogMessage, caller protocol.SessionID) {
	profile, err := d.resolveGardenProfile(caller, protocol.Deref(msg.ProfileID), "")
	var calls []protocol.ModelCall
	if err == nil {
		calls, err = d.store.ModelCalls(profile.ID, strings.TrimSpace(protocol.Deref(msg.Sender)))
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, ModelCalls: calls})
}

func (d *Daemon) modelCallLogPruneHandler(_ context.Context, _ *jobs.Job) (any, error) {
	pruned, err := d.store.PruneModelCalls(d.clock.Now().Add(-modelCallLogRetention))
	if err != nil {
		return nil, fmt.Errorf("pruning the model call log: %w", err)
	}
	return map[string]any{"pruned": pruned}, nil
}
