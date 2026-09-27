package classifier

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseResponse_Waiting(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"WAITING", "waiting_input"},
		{"waiting", "waiting_input"},
		{"WAITING\n", "waiting_input"},
		{"  WAITING  ", "waiting_input"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseResponse_Parked(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"PARKED", VerdictParked},
		{"parked", VerdictParked},
		{`{"verdict":"PARKED"}`, VerdictParked},
		{"Verdict: PARKED", VerdictParked},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseResponse_Done(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"DONE", "idle"},
		{"done", "idle"},
		{"DONE\n", "idle"},
		{"anything else", "idle"},
		{"", "idle"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseResponse_MixedCase(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"Waiting", "waiting_input"},
		{"WaItInG", "waiting_input"},
		{"Done", "idle"},
		{"DoNe", "idle"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseResponse_SurroundingText(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{"WAITING line prefix", "WAITING - asks a follow-up", "waiting_input"},
		{"multi-line waiting with rationale", "WAITING\nThe text ends with a direct question.", "waiting_input"},
		{"verdict label with done", "Verdict: DONE", "idle"},
		{"verdict label with waiting", "verdict = waiting", "waiting_input"},
		{"DONE line prefix", "DONE (completed)", "idle"},
		{"multi-line with final verdict", "analysis...\nDONE", "idle"},
		{"sentence without explicit verdict prefix", "The assistant is waiting for user input", "idle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResponse(tt.response)
			if got != tt.want {
				t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
			}
		})
	}
}

func TestParseResponse_NoStandaloneToken(t *testing.T) {
	got := ParseResponse("This appears complete without further input.")
	if got != "idle" {
		t.Errorf("expected idle when no WAITING/DONE prefix, got %q", got)
	}
}

func TestParseResponse_JSONStructured(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{"json verdict waiting", `{"verdict":"WAITING"}`, "waiting_input"},
		{"json state done", `{"state":"DONE"}`, "idle"},
		{"json status idle", `{"status":"IDLE"}`, "idle"},
		{"json needs_input true", `{"needs_input":true}`, "waiting_input"},
		{"fenced json verdict waiting", "```json\n{\"verdict\":\"WAITING\"}\n```", "waiting_input"},
		{"fenced json verdict done", "```json\n{\"verdict\":\"DONE\"}\n```", "idle"},
		{"bulleted rubric line then verdict", "- WAITING means asks a question\nVerdict: DONE", "idle"},
		{"invalid json no verdict", `{"foo":"bar"}`, "idle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResponse(tt.response)
			if got != tt.want {
				t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
			}
		})
	}
}

func TestParseVerdictFromResponse_ExplicitVerdictRequired(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
		ok       bool
	}{
		{"plain sentence no explicit verdict", "The assistant is waiting for user input", "", false},
		{"explicit waiting verdict", "WAITING\nbecause it asks a question", "waiting_input", true},
		{"explicit done verdict", "Verdict: DONE", "idle", true},
		{"json verdict", `{"verdict":"DONE"}`, "idle", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseVerdictFromResponse(tt.response)
			if ok != tt.ok {
				t.Fatalf("parseVerdictFromResponse(%q) ok=%v, want %v", tt.response, ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("parseVerdictFromResponse(%q) = %q, want %q", tt.response, got, tt.want)
			}
		})
	}
}

func TestParseResponse_EmptyAndWhitespace(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"", "idle"},
		{"   ", "idle"},
		{"\n\n", "idle"},
		{"\t\t", "idle"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseVerdict_ReadsStructuredOutput(t *testing.T) {
	result, ok := ParseVerdict(json.RawMessage(`{"verdict":"DONE"}`))
	if !ok {
		t.Fatal("expected ParseVerdict to return a verdict")
	}
	if result != "idle" {
		t.Fatalf("result = %q, want idle", result)
	}
}

func TestParseVerdict_NoStructuredVerdict(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"unrelated":true}`)} {
		if result, ok := ParseVerdict(raw); ok {
			t.Fatalf("ParseVerdict(%s) = %q, want no verdict", raw, result)
		}
	}
}

func TestParseVerdictFromCodexJSONL(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"type":"thread.started","thread_id":"abc"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"verdict\":\"DONE\"}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1}}`,
	}, "\n")

	got, ok := parseVerdictFromCodexJSONL([]byte(jsonl))
	if !ok {
		t.Fatal("expected parseVerdictFromCodexJSONL to parse verdict")
	}
	if got != "idle" {
		t.Fatalf("parseVerdictFromCodexJSONL() = %q, want idle", got)
	}
}

func TestParseCodexErrorFromJSONL(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"type":"thread.started","thread_id":"abc"}`,
		`{"type":"error","message":"model_not_found"}`,
	}, "\n")
	got := parseCodexErrorFromJSONL([]byte(jsonl))
	if got != "model_not_found" {
		t.Fatalf("parseCodexErrorFromJSONL() = %q, want model_not_found", got)
	}
}

func TestParseCodexErrorFromJSONL_LargeLine(t *testing.T) {
	line, err := json.Marshal(map[string]any{
		"type":    "error",
		"message": strings.Repeat("x", 70*1024) + "model_not_found",
	})
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}

	got := parseCodexErrorFromJSONL(append(line, '\n'))
	if !strings.HasSuffix(got, "model_not_found") {
		t.Fatalf("parseCodexErrorFromJSONL() suffix mismatch: %q", got)
	}
}

func TestParseVerdictFromCodexJSONL_LargeLine(t *testing.T) {
	line, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_0",
			"type": "agent_message",
			"text": strings.Repeat("x", 70*1024) + `{"verdict":"DONE"}`,
		},
	})
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}

	got, ok := parseVerdictFromCodexJSONL(append(line, '\n'))
	if !ok {
		t.Fatal("expected parseVerdictFromCodexJSONL to parse verdict from large line")
	}
	if got != "idle" {
		t.Fatalf("parseVerdictFromCodexJSONL() = %q, want idle", got)
	}
}
