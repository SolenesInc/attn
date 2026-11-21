package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const integersPastFloatPrecision = `{"record":{"id":9007199254740993,"parent":{"id":-9007199254740993}}}`

func TestAManualRunFromTheCLIGivesTheAgentItsInputFilesIntegersExactly(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	definition, input := s.Path("check.yaml"), s.Path("input.json")
	if err := os.MkdirAll(filepath.Dir(definition), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Check\ntrigger: {type: manual}\nprompt: Check the record.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", s.Dir)
	if err := os.WriteFile(definition, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte(integersPastFloatPrecision), 0o600); err != nil {
		t.Fatal(err)
	}
	var applied protocol.AutomationDefinitionSummary
	s.Attn("automation", "apply", "--file", definition).JSON(t, &applied)
	var run protocol.AutomationRunSummary
	s.Attn("automation", "run", strconv.Itoa(applied.ID), "--input-file", input).JSON(t, &run)
	requireSameJSONNumbers(t, s.Launched(string(protocol.Deref(run.SessionID))), filepath.Join(s.Dir, "automation", "occurrences", run.ID+".json"), integersPastFloatPrecision)
}

func requireSameJSONNumbers(t *testing.T, agent *fakeagent.Run, occurrence, want string) {
	t.Helper()
	if prompt := agent.Prompted(); !strings.Contains(prompt, occurrence) {
		t.Fatalf("the agent was prompted with %q, want it pointed at %s", prompt, occurrence)
	}
	stored, err := os.ReadFile(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(numbersKept(t, stored), numbersKept(t, []byte(want))) {
		t.Fatalf("the agent's occurrence file holds %s, want %s", stored, want)
	}
}

func numbersKept(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return value
}
