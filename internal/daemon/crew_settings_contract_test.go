package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

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
