package agent

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClaudeModelCatalog(t *testing.T) {
	stream := `{"type":"keep_alive"}
{"type":"control_response","response":{"subtype":"error","request_id":"unrelated"}}
{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{"models":[
{"value":"sonnet","resolvedModel":"claude-sonnet-5","displayName":"Sonnet","supportsEffort":true,"supportedEffortLevels":["low","high","future-level"]},
{"value":"no-effort","supportsEffort":false},
{"value":"unknown"}
]}}}`
	models, err := readClaudeModels(strings.NewReader(stream), "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 || models[0].ResolvedModel != "claude-sonnet-5" {
		t.Fatalf("models = %#v", models)
	}
	if models[0].SupportsEffort == nil || !*models[0].SupportsEffort || models[0].SupportedEffortLevels[2] != "future-level" {
		t.Fatalf("effort metadata = %#v", models[0])
	}
	if models[1].SupportsEffort == nil || *models[1].SupportsEffort || models[2].SupportsEffort != nil {
		t.Fatalf("unknown and unsupported collapsed: %#v", models)
	}
}

func TestClaudeModelCatalogFailures(t *testing.T) {
	for _, test := range []struct{ name, stream, want string }{
		{"EOF", "", "EOF"},
		{"invalid JSON", "not json", "read Claude model catalog"},
		{"error", `{"type":"control_response","response":{"subtype":"error","request_id":"catalog","error":"private child output"}}`, "initialization failed"},
		{"missing models", `{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{}}}`, "did not supply"},
		{"missing ID", `{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{"models":[{}]}}}`, "missing model ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := readClaudeModels(strings.NewReader(test.stream), "catalog")
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "private child output") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	models, err := readClaudeModels(strings.NewReader(`{"type":"control_response","response":{"subtype":"success","request_id":"catalog","response":{"models":[]}}}`), "catalog")
	if err != nil || models == nil || len(models) != 0 {
		t.Fatalf("empty catalog = %#v, %v", models, err)
	}
	_, err = readClaudeModels(strings.NewReader(`{"type":"keep_alive"}`), "catalog")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}
