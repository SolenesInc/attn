package classifier

import (
	"encoding/json"
	"testing"
)

func assertVerdict(t *testing.T, response, want string) {
	t.Helper()
	got, ok := parseVerdictFromResponse(response)
	if got != want || ok != (want != "") {
		t.Errorf("parseVerdictFromResponse(%q) = %q, %v, want %q", response, got, ok, want)
	}
}

func TestParseVerdictFromResponse_Waiting(t *testing.T) {
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
		assertVerdict(t, tt.response, tt.want)
	}
}

func TestParseVerdictFromResponse_Parked(t *testing.T) {
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
		assertVerdict(t, tt.response, tt.want)
	}
}

func TestParseVerdictFromResponse_Done(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"DONE", "idle"},
		{"done", "idle"},
		{"DONE\n", "idle"},
		{"anything else", ""},
		{"", ""},
	}

	for _, tt := range tests {
		assertVerdict(t, tt.response, tt.want)
	}
}

func TestParseVerdictFromResponse_SurroundingText(t *testing.T) {
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
		{"sentence without explicit verdict prefix", "The assistant is waiting for user input", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertVerdict(t, tt.response, tt.want)
		})
	}
}

func TestParseVerdictFromResponse_JSONStructured(t *testing.T) {
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
		{"invalid json no verdict", `{"foo":"bar"}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertVerdict(t, tt.response, tt.want)
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
