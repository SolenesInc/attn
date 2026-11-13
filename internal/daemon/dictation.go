package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
)

const DictationRefineDeadlineTimer stepclock.Name = "dictation-refine-deadline"

const dictationRefineBaseLimit = 15 * time.Second

func dictationRefineLimit(recording time.Duration) time.Duration {
	return dictationRefineBaseLimit + recording/4
}

type dictationRefiner struct {
	agent         string
	fallbackModel string
	effort        string
}

var dictationRefiners = map[string]dictationRefiner{
	"claude": {agent: "claude", fallbackModel: "haiku"},
	"codex":  {agent: "codex", fallbackModel: "gpt-6-luna", effort: "low"},
}

const dictationRefineDefaultAgent = "claude"

func parseDictationRefinementAgent(raw string) (dictationRefiner, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		name = dictationRefineDefaultAgent
	}
	refiner, ok := dictationRefiners[name]
	if !ok {
		return dictationRefiner{}, fmt.Errorf("dictation refinement supports claude or codex, not %q", raw)
	}
	return refiner, nil
}

type dictationRefineRequest struct {
	id        string
	profileID string
	rawText   string
	recording time.Duration
	format    protocol.DictationTextFormat
}

func parseDictationRefineRequest(msg *protocol.DictationRefineMessage, selectedProfile string) (dictationRefineRequest, error) {
	id, profileID := strings.TrimSpace(msg.RequestID), strings.TrimSpace(msg.ProfileID)
	switch {
	case id == "":
		return dictationRefineRequest{}, errors.New("dictation refinement needs a request_id")
	case profileID == "":
		return dictationRefineRequest{}, errors.New("dictation refinement needs a profile_id")
	case selectedProfile != "" && profileID != selectedProfile:
		return dictationRefineRequest{}, fmt.Errorf("dictation refinement for profile %s came from an app showing profile %s", profileID, selectedProfile)
	}
	request, err := newDictationRefineRequest(profileID, msg.RawText, msg.RecordingDurationMs, msg.Format)
	request.id = id
	return request, err
}

func newDictationRefineRequest(profileID, rawText string, recordingMs int, format protocol.DictationTextFormat) (dictationRefineRequest, error) {
	request := dictationRefineRequest{profileID: profileID, rawText: rawText, recording: time.Duration(recordingMs) * time.Millisecond, format: format}
	switch {
	case strings.TrimSpace(rawText) == "":
		return request, errors.New("dictation refinement needs raw_text")
	case recordingMs < 0:
		return request, fmt.Errorf("recording_duration_ms must not be negative, got %d", recordingMs)
	}
	switch format {
	case protocol.DictationTextFormatPlain, protocol.DictationTextFormatMarkdown:
	default:
		return request, fmt.Errorf("dictation refinement format must be plain or markdown, not %q", format)
	}
	return request, nil
}

type dictationRefineKey struct {
	client    *wsClient
	requestID string
}

type dictationRefinements struct {
	mu      sync.Mutex
	pending map[dictationRefineKey]context.CancelFunc
}

func (r *dictationRefinements) admit(key dictationRefineKey, cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.pending[key]; taken {
		return false
	}
	if r.pending == nil {
		r.pending = make(map[dictationRefineKey]context.CancelFunc)
	}
	r.pending[key] = cancel
	return true
}

func (r *dictationRefinements) settle(key dictationRefineKey) bool {
	r.mu.Lock()
	cancel, owned := r.pending[key]
	delete(r.pending, key)
	r.mu.Unlock()
	if owned {
		cancel()
	}
	return owned
}

func (r *dictationRefinements) dropClient(client *wsClient) {
	r.mu.Lock()
	var dropped []context.CancelFunc
	for key, cancel := range r.pending {
		if key.client == client {
			dropped = append(dropped, cancel)
			delete(r.pending, key)
		}
	}
	r.mu.Unlock()
	for _, cancel := range dropped {
		cancel()
	}
}

func (d *Daemon) handleDictationRefine(client *wsClient, msg *protocol.DictationRefineMessage) {
	result := protocol.DictationRefineResultMessage{Event: protocol.EventDictationRefineResult, RequestID: msg.RequestID, ProfileID: msg.ProfileID}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	if !client.isTrustedAppClient() {
		fail(errors.New("dictation refinement requires the authenticated attn app"))
		return
	}
	request, err := parseDictationRefineRequest(msg, client.selectedProfile())
	if err != nil {
		fail(err)
		return
	}
	ctx, cancel := context.WithCancel(d.life.Context())
	key := dictationRefineKey{client: client, requestID: request.id}
	if !d.dictationRefinements.admit(key, cancel) {
		cancel()
		fail(fmt.Errorf("dictation refinement %s is already running; retry with a new request_id", request.id))
		return
	}
	answering := d.life.Go("dictationRefineAnswer", func() {
		answer := d.refineWithinLimit(ctx, request)
		stopped := ctx.Err() != nil
		if !d.dictationRefinements.settle(key) || stopped {
			return
		}
		result.Success = answer.err == nil
		result.RefinedText = answer.refinedText()
		result.Error = answer.errorText()
		result.LimitMs = answer.limitMs
		d.sendToClient(client, result)
	})
	if !answering {
		d.dictationRefinements.settle(key)
	}
}

func (d *Daemon) handleDictationRefineCancel(client *wsClient, msg *protocol.DictationRefineCancelMessage) {
	d.dictationRefinements.settle(dictationRefineKey{client: client, requestID: strings.TrimSpace(msg.RequestID)})
}

func spokenSeconds(duration time.Duration) string {
	seconds := float64(duration.Round(100*time.Millisecond)) / float64(time.Second)
	return strconv.FormatFloat(seconds, 'f', -1, 64) + " s"
}

type dictationRefineAnswer struct {
	text    string
	err     error
	limitMs *int
}

func (a dictationRefineAnswer) refinedText() *string {
	if a.err != nil {
		return nil
	}
	return protocol.Ptr(a.text)
}

func (a dictationRefineAnswer) errorText() *string {
	if a.err == nil {
		return nil
	}
	return protocol.Ptr(a.err.Error())
}

func (d *Daemon) refineWithinLimit(ctx context.Context, request dictationRefineRequest) dictationRefineAnswer {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	limit := dictationRefineLimit(request.recording)
	deadline := d.clock.NewTimer(DictationRefineDeadlineTimer, limit)
	defer deadline.Stop()
	answered := make(chan dictationRefineAnswer, 1)
	if !d.life.Go("dictationRefine", func() { answered <- d.runDictationRefine(ctx, request) }) {
		return dictationRefineAnswer{err: errDaemonStopping}
	}
	select {
	case answer := <-answered:
		if answer.err != nil {
			answer.err = fmt.Errorf("refinement failed: %w. Original kept", answer.err)
		}
		return answer
	case <-deadline.C():
		return dictationRefineAnswer{
			err:     fmt.Errorf("refinement did not answer within %s for this %s recording. Original kept", spokenSeconds(limit), spokenSeconds(request.recording)),
			limitMs: protocol.Ptr(int(limit.Milliseconds())),
		}
	case <-ctx.Done():
		return dictationRefineAnswer{err: ctx.Err()}
	}
}

func (d *Daemon) runDictationRefine(ctx context.Context, request dictationRefineRequest) dictationRefineAnswer {
	if !headless.Enabled() {
		return dictationRefineAnswer{err: headless.Refusal("dictation refinement")}
	}
	refiner, err := parseDictationRefinementAgent(d.setting(setting.DictationRefinementAgent))
	if err != nil {
		return dictationRefineAnswer{err: err}
	}
	driver := agentdriver.Get(refiner.agent)
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return dictationRefineAnswer{err: fmt.Errorf("%s is not installed as a harness that runs headless tasks", refiner.agent)}
	}
	executable, err := d.machineAgentExecutable(driver, "")
	if err != nil {
		return dictationRefineAnswer{err: fmt.Errorf("%s executable was not found: %w", refiner.agent, err)}
	}
	model := d.resolveTierModel(ctx, refiner.agent, executable, modeltiers.Light, "", refiner.fallbackModel)
	workDir, err := os.MkdirTemp("", "attn-dictation-refine-*")
	if err != nil {
		return dictationRefineAnswer{err: fmt.Errorf("create refinement scratch directory: %w", err)}
	}
	defer os.RemoveAll(workDir)
	transcript, err := json.Marshal(map[string]string{"transcript": request.rawText})
	if err != nil {
		return dictationRefineAnswer{err: err}
	}
	run, err := provider.RunHeadlessTask(ctx, agentdriver.HeadlessTaskRequest{
		Executable:      executable,
		Model:           model,
		ReasoningEffort: refiner.effort,
		SystemPrompt:    prompts.RenderText("dictation-refiner", string(request.format), nil),
		Prompt:          string(transcript),
		WorkDir:         workDir,
		DisableTools:    true,
	})
	if err != nil {
		cause := run.Diagnostics
		if cause == "" {
			cause = err.Error()
		}
		return dictationRefineAnswer{err: fmt.Errorf("%s: %s", refiner.agent, cause)}
	}
	text := strings.TrimSpace(run.Text)
	if text == "" {
		return dictationRefineAnswer{err: fmt.Errorf("%s returned no text", refiner.agent)}
	}
	return dictationRefineAnswer{text: text}
}
