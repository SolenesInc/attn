package fakeagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

type ephemeralKey struct {
	conn *appServerConn
	id   uint64
}

func (s *appServer) interruptEphemeral(c *appServerConn, id uint64) {
	s.mu.Lock()
	process := s.ephemeral[ephemeralKey{c, id}]
	s.mu.Unlock()
	if process != nil {
		process.close()
	}
}

type ephemeralRun struct {
	Prompt   string          `json:"prompt"`
	System   string          `json:"system"`
	Schema   json.RawMessage `json:"schema"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Effort   string          `json:"effort"`
}

func (s *appServer) runEphemeral(c *appServerConn, id uint64, raw json.RawMessage) {
	var run ephemeralRun
	_ = json.Unmarshal(raw, &run)
	process, err := dialControl(s.cfg, nil)
	if err != nil {
		c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"_tag": "Cause", "code": 0, "message": err.Error()}})
		return
	}
	process.start()
	s.mu.Lock()
	s.ephemeral[ephemeralKey{c, id}] = process
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.ephemeral, ephemeralKey{c, id})
		s.mu.Unlock()
		process.close()
	}()
	model := run.Model
	if run.Provider != "" {
		model = run.Provider + "/" + run.Model
	}
	asked := headlessTask{Harness: s.flavor.harness, Prompt: joinSystemPrompt(run.System, run.Prompt), Model: model, Effort: run.Effort,
		launchedAs: launchedAs{Argv: os.Args, Env: os.Environ()}}
	var answer headlessAnswer
	if err := process.call(context.Background(), methodHeadless, asked, &answer); errors.Is(err, errPeerClosed) {
		return
	} else if err != nil {
		answer.Failure = err.Error()
	}
	result := map[string]any{"text": answer.Text, "provider": run.Provider, "model": run.Model, "turns": 1,
		"usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"total": 0}}}
	if answer.Failure == "" && len(run.Schema) > 0 {
		if json.Valid([]byte(answer.Text)) {
			result["output"] = json.RawMessage(answer.Text)
		} else {
			answer.Failure = "the answer is not JSON: " + answer.Text
		}
	}
	if answer.Failure != "" {
		c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{
			"_tag": "Cause", "code": 0, "message": answer.Failure,
			"data": []any{map[string]any{"_tag": "Fail", "error": map[string]any{"_tag": "EphemeralFailed", "message": answer.Failure}}},
		}})
		return
	}
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
