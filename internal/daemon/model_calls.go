package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
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

var modelCallTierFallbacks = map[string]map[modeltiers.Tier]string{
	"claude": {modeltiers.Light: "haiku", modeltiers.Standard: "sonnet", modeltiers.Deep: "opus"},
	"codex":  {modeltiers.Light: "gpt-6-luna", modeltiers.Standard: "gpt-5.6-terra", modeltiers.Deep: "gpt-6.1-sol"},
}

type modelCallTarget struct {
	harness    string
	provider   agentdriver.HeadlessTaskProvider
	driver     agentdriver.Driver
	executable string
	model      string
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

func modelCallHarnesses() []string {
	var names []string
	for _, name := range agentdriver.List() {
		if ok, _ := agentdriver.HeadlessTaskAvailability(agentdriver.Get(name)); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func validateModelCallsAgent(value string) error {
	name := strings.ToLower(strings.TrimSpace(value))
	if name == "" || slices.Contains(modelCallHarnesses(), name) {
		return nil
	}
	return fmt.Errorf("model calls are answered by %s, not %q", strings.Join(modelCallHarnesses(), " or "), value)
}

func (d *Daemon) modelCallDefaultHarness() string {
	if name := strings.ToLower(strings.TrimSpace(d.setting(setting.ModelCallsAgent))); name != "" {
		return name
	}
	return d.newSessionAgent()
}

func (d *Daemon) modelCallHarness(named string) (agentdriver.Driver, agentdriver.HeadlessTaskProvider, error) {
	name := strings.ToLower(strings.TrimSpace(named))
	if name == "" {
		name = d.modelCallDefaultHarness()
	}
	installed := modelCallHarnesses()
	if !slices.Contains(installed, name) {
		return nil, nil, d.modelCallHarnessRefusal(named, name, installed)
	}
	driver := agentdriver.Get(name)
	return driver, driver.(agentdriver.HeadlessTaskProvider), nil
}

func (d *Daemon) modelCallHarnessRefusal(named, name string, installed []string) error {
	answering := strings.Join(installed, " or ")
	if _, headless := agentdriver.Get(name).(agentdriver.HeadlessTaskProvider); headless {
		return fmt.Errorf("%s is not installed; model calls are answered by %s", name, answering)
	}
	if strings.TrimSpace(named) == "" && strings.TrimSpace(d.setting(setting.ModelCallsAgent)) == "" {
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
	driver, provider, err := d.modelCallHarness(protocol.Deref(msg.Harness))
	if err != nil {
		return modelCallTarget{}, err
	}
	executable, err := d.machineAgentExecutable(driver, "")
	if err != nil {
		return modelCallTarget{}, fmt.Errorf("%s executable was not found: %w", driver.Name(), err)
	}
	model := choice.model
	if model == "" {
		model = d.resolveTierModel(ctx, driver.Name(), executable, choice.tier, "", modelCallTierFallbacks[driver.Name()][choice.tier])
	}
	return modelCallTarget{harness: driver.Name(), provider: provider, driver: driver, executable: executable, model: model}, nil
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
	result  agentdriver.ModelCallResult
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
		if outcome.result.ModelTime > 0 {
			response.ModelMs = protocol.Ptr(int(outcome.result.ModelTime.Milliseconds()))
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
	request := agentdriver.ModelCallRequest{
		Executable:   target.executable,
		Model:        target.model,
		SystemPrompt: msg.System,
		Input:        msg.Input,
	}
	if spare := d.modelCallSpare.take(d, target.harness); spare != nil {
		bySpare.Store(true)
		result, err := spare.Answer(ctx, request)
		if !errors.Is(err, agentdriver.ErrSpareUnusable) {
			return modelCallOutcome{target: target, result: result, err: err}
		}
		bySpare.Store(false)
		d.logf("model call: the %s spare could not answer, so the call runs cold: %v", target.harness, err)
	}
	workDir, err := os.MkdirTemp("", "attn-model-call-*")
	if err != nil {
		return modelCallOutcome{err: fmt.Errorf("create the call's scratch directory: %w", err)}
	}
	defer os.RemoveAll(workDir)
	request.WorkDir = workDir
	var result agentdriver.ModelCallResult
	if socket := d.codexShared().runningSocket(profile); target.harness == "codex" && socket != "" {
		result, err = answerModelCallOnSharedCodex(ctx, socket, request)
	} else {
		result, err = agentdriver.AnswerModelCall(ctx, target.provider, request)
	}
	if err != nil {
		err = fmt.Errorf("%s: %w", target.harness, err)
	}
	return modelCallOutcome{target: target, result: result, err: err}
}

func (d *Daemon) logModelCall(sender, purpose string, target modelCallTarget, bySpare bool, took time.Duration, outcome modelCallOutcome) {
	result := "answered"
	if outcome.err != nil {
		result = "failed: " + outcome.err.Error()
	}
	d.logf("model call: sender=%q purpose=%q harness=%s model=%s spare=%t took=%s model_ms=%d tokens_in=%d tokens_out=%d cost_usd=%.4f outcome=%s",
		sender, purpose, target.harness, target.model, bySpare, took.Round(time.Millisecond), outcome.result.ModelTime.Milliseconds(),
		outcome.result.InputTokens, outcome.result.OutputTokens, outcome.result.CostUSD, result)
}

func (d *Daemon) recordModelCall(sender *store.PairedSender, purpose string, outcome modelCallOutcome, arrived time.Time, took time.Duration, ended protocol.ModelCallOutcome) {
	call := protocol.ModelCall{
		At:        store.ModelCallTime(arrived),
		Sender:    sender.Name,
		Purpose:   purpose,
		Harness:   outcome.target.harness,
		Model:     outcome.target.model,
		TookMs:    int(took.Milliseconds()),
		TokensIn:  outcome.result.InputTokens,
		TokensOut: outcome.result.OutputTokens,
		CostUsd:   outcome.result.CostUSD,
		Outcome:   ended,
	}
	if outcome.result.ModelTime > 0 {
		call.ModelMs = protocol.Ptr(int(outcome.result.ModelTime.Milliseconds()))
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
