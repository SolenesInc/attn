package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func readCrewSetResult(t *testing.T, client *wsClient) protocol.CrewSetResultMessage {
	t.Helper()
	select {
	case raw := <-client.send:
		var result protocol.CrewSetResultMessage
		if err := json.Unmarshal(raw.payload, &result); err != nil {
			t.Fatalf("decode crew set result: %v", err)
		}
		return result
	default:
		t.Fatal("missing crew set response")
		return protocol.CrewSetResultMessage{}
	}
}

func TestCrewSet_EffortPersistsClearsAndReachesTheLaunch(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.store.SetSetting(SettingDefaultEffortPrefix+"claude", "medium")

	set := crewSet(t, d, protocol.CrewSetMessage{Member: "trellis", Effort: protocol.Ptr("HIGH")})
	if !set.Ok {
		t.Fatalf("set effort: %v", protocol.Deref(set.Error))
	}
	member := set.CrewSetResult.Member
	if protocol.Deref(member.Effort) != "high" || protocol.Deref(member.ResolvedEffort) != "high" {
		t.Fatalf("set result stored/resolved effort = %q/%q, want high/high", protocol.Deref(member.Effort), protocol.Deref(member.ResolvedEffort))
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").Effort); got != "high" {
		t.Fatalf("persisted effort = %q, want high", got)
	}
	if _, err := d.crewWake("trellis", ""); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if got := spawnedSessions(t, backend)[0].Effort; got != "high" {
		t.Fatalf("spawn effort = %q, want high", got)
	}

	cleared := crewSet(t, d, protocol.CrewSetMessage{Member: "trellis", Effort: protocol.Ptr("")})
	if !cleared.Ok {
		t.Fatalf("clear effort: %v", protocol.Deref(cleared.Error))
	}
	member = cleared.CrewSetResult.Member
	if member.Effort != nil || protocol.Deref(member.ResolvedEffort) != "medium" {
		t.Fatalf("cleared stored/resolved effort = %v/%q, want unset/medium", member.Effort, protocol.Deref(member.ResolvedEffort))
	}
}

func TestCrewSet_HarnessChangeClearsPinsAndRejectedSelectionsAreAtomic(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	if _, err := d.updateCrewMember("keel", func(member *crew.Member) (bool, error) {
		member.Model = "claude-model"
		member.Effort = "high"
		return true, nil
	}); err != nil {
		t.Fatalf("seed launch pins: %v", err)
	}

	changed := crewSet(t, d, protocol.CrewSetMessage{Member: "keel", Agent: protocol.Ptr("codex")})
	if !changed.Ok {
		t.Fatalf("change harness: %v", protocol.Deref(changed.Error))
	}
	if changed.CrewSetResult.Member.Model != nil || changed.CrewSetResult.Member.Effort != nil {
		t.Fatalf("harness change retained model/effort: %+v", changed.CrewSetResult.Member)
	}

	d.store.SetSetting(canonicalExecutableSettingKey("claude"), "/missing/claude-for-crew-test")
	before := changed.CrewSetResult.Member
	rejected := crewSet(t, d, protocol.CrewSetMessage{
		Member: "keel", Agent: protocol.Ptr("claude"), Model: protocol.Ptr("custom"), Effort: protocol.Ptr("max"),
	})
	if rejected.Ok || !strings.Contains(protocol.Deref(rejected.Error), "unavailable") {
		t.Fatalf("unavailable harness result = ok:%t error:%q", rejected.Ok, protocol.Deref(rejected.Error))
	}
	after := memberByID(t, crewList(t, d), "keel")
	if after.Revision != before.Revision || protocol.Deref(after.Agent) != "codex" || after.Model != nil || after.Effort != nil {
		t.Fatalf("rejected atomic update changed the member: before=%+v after=%+v", before, after)
	}
}

func TestCrewSet_CustomModelsSurviveDiscoveryAndKnownEffortIsValidated(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	dir := t.TempDir()
	executable := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nread request\nprintf '%s\\n' '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"attn-model-discovery\",\"response\":{\"models\":[{\"value\":\"known\",\"supportsEffort\":true,\"supportedEffortLevels\":[\"medium\",\"high\"]}]}}}'\ncat >/dev/null\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("write discovery harness: %v", err)
	}
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), executable)

	custom := crewSet(t, d, protocol.CrewSetMessage{
		Member: "alder", Model: protocol.Ptr("private-model"), Effort: protocol.Ptr("high"),
	})
	if !custom.Ok || protocol.Deref(custom.CrewSetResult.Member.Model) != "private-model" {
		t.Fatalf("custom model result = %+v", custom)
	}

	rejected := crewSet(t, d, protocol.CrewSetMessage{
		Member: "alder", Model: protocol.Ptr("known"), Effort: protocol.Ptr("max"),
	})
	if rejected.Ok || !strings.Contains(protocol.Deref(rejected.Error), "does not support effort \"max\"") {
		t.Fatalf("unsupported effort result = ok:%t error:%q", rejected.Ok, protocol.Deref(rejected.Error))
	}
	after := memberByID(t, crewList(t, d), "alder")
	if protocol.Deref(after.Model) != "private-model" || protocol.Deref(after.Effort) != "high" {
		t.Fatalf("rejected model/effort pair changed storage: %+v", after)
	}
}

func TestCrewSet_WebSocketCorrelatesSuccessConflictAndMissingRequestID(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	initial := memberByID(t, crewList(t, d), "alder")
	client := newInternalWSClient()

	d.handleCrewSetWS(client, &protocol.CrewSetMessage{
		Cmd: protocol.CmdCrewSet, Member: "alder", RequestID: protocol.Ptr("save-1"),
		ExpectedRevision: protocol.Ptr(initial.Revision), Effort: protocol.Ptr("high"),
	})
	saved := readCrewSetResult(t, client)
	if !saved.Success || saved.Conflict || saved.RequestID != "save-1" || saved.Member == nil || saved.Member.Revision <= initial.Revision {
		t.Fatalf("save result = %+v", saved)
	}

	d.handleCrewSetWS(client, &protocol.CrewSetMessage{
		Cmd: protocol.CmdCrewSet, Member: "alder", RequestID: protocol.Ptr("save-stale"),
		ExpectedRevision: protocol.Ptr(initial.Revision), Effort: protocol.Ptr("low"),
	})
	conflict := readCrewSetResult(t, client)
	if conflict.Success || !conflict.Conflict || conflict.RequestID != "save-stale" || conflict.Member == nil || conflict.Member.Revision != saved.Member.Revision {
		t.Fatalf("conflict result = %+v", conflict)
	}
	if got := protocol.Deref(conflict.Member.Effort); got != "high" {
		t.Fatalf("conflict returned effort %q, want authoritative high", got)
	}

	d.handleCrewSetWS(client, &protocol.CrewSetMessage{
		Cmd: protocol.CmdCrewSet, Member: "alder", Effort: protocol.Ptr("max"),
	})
	missing := readCrewSetResult(t, client)
	if missing.Success || missing.RequestID != "" || !strings.Contains(protocol.Deref(missing.Error), "request id") {
		t.Fatalf("missing-id result = %+v", missing)
	}
	current := memberByID(t, crewList(t, d), "alder")
	if current.Revision != saved.Member.Revision || protocol.Deref(current.Effort) != "high" {
		t.Fatalf("missing-id write reached storage: %+v", current)
	}
}
