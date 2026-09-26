package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
)

func configuredBuild(t *testing.T, d *Daemon) delegationprefs.Config {
	t.Helper()
	roles := prompts.ExpandDelegationRoles(prompts.DelegationRoleTemplates())
	build := roles[1]
	build.ID = "build"
	build.Builtin = nil
	build.Choices[0].Selection = delegationprefs.Selection{Harness: "codex"}
	build.Instructions = "Check {{literal}} carefully"
	roles = []protocol.DelegationRole{build}
	cfg, err := d.store.SaveDelegationPreferences(delegationprefs.Config{Enabled: true, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestWorkflowSkillUnsupportedHarnessOnlyBlocksMaintainedRoles(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())
	t.Setenv("ATTN_INSTANCE", "dev")
	d := newDelegationDaemon(t)
	cfg := configuredBuild(t, d)
	cfg.WorkflowSkillEnabled = true
	cfg.Roles[0].Choices[0].Selection = delegationprefs.Selection{Harness: "custom-plugin"}
	cfg.Fallback.Selection = delegationprefs.Selection{Harness: "custom-plugin"}
	maintained := prompts.DelegationRoleTemplates()[0]
	maintained.Choices[0].Selection = delegationprefs.Selection{Harness: "custom-plugin"}
	cfg.Roles = append(cfg.Roles, maintained)
	if _, err := d.store.SaveDelegationPreferences(cfg); err != nil {
		t.Fatal(err)
	}
	for _, request := range []delegationprefs.Request{{Role: "build"}, {Fallback: true}, {Role: maintained.ID}} {
		resolved, err := delegationprefs.Resolve(prompts.ExpandDelegationPreferences(cfg), request)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(resolved)
		if err != nil {
			t.Fatal(err)
		}
		var accepted delegationprefs.Resolved
		if err := json.Unmarshal(raw, &accepted); err != nil {
			t.Fatal(err)
		}
		err = d.ensureDelegationWorkflowSkill(&accepted)
		if request.Role == maintained.ID {
			if err == nil || !strings.Contains(err.Error(), "no supported") {
				t.Fatalf("maintained role error = %v", err)
			}
		} else if err != nil {
			t.Fatalf("custom/fallback launch blocked: %v", err)
		}
	}
}

func TestDelegationDiscoveryAndEffortValidation(t *testing.T) {
	d := newDaemonForTest(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "queried")
	executable := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ntouch '" + marker + "'\nread request\nprintf '%s\\n' '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"attn-model-discovery\",\"response\":{\"models\":[{\"value\":\"known\",\"supportsEffort\":true,\"supportedEffortLevels\":[\"medium\",\"high\"]}]}}}'\ncat >/dev/null\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), executable)
	if catalog, err := d.discoverDelegationModels(context.Background(), "claude"); err != nil || len(catalog.Models) != 1 {
		t.Fatalf("discovery while preferences are off: %+v %v", catalog, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("discovery did not query the harness")
	}
	cfg := configuredBuild(t, d)
	cfg.Roles[0].Choices[0].Selection = delegationprefs.Selection{Harness: "claude", Model: "known", Effort: "high"}
	if _, err := d.store.SaveDelegationPreferences(cfg); err != nil {
		t.Fatal(err)
	}
	catalog, err := d.discoverDelegationModels(context.Background(), "claude")
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].Access != "unknown" {
		t.Fatalf("%+v %v", catalog, err)
	}
	if _, err := d.resolveDelegationPreferences(&protocol.DelegateMessage{Role: protocol.Ptr("build"), Effort: protocol.Ptr("max")}); err == nil {
		t.Fatal("unsupported model effort accepted")
	}
	got, err := d.resolveDelegationPreferences(&protocol.DelegateMessage{Role: protocol.Ptr("build"), Effort: protocol.Ptr("medium")})
	if err != nil || got.Selection.Effort != "medium" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = d.resolveDelegationPreferences(&protocol.DelegateMessage{Role: protocol.Ptr("build"), Model: protocol.Ptr("custom")})
	if err != nil || got.Selection.Model != "custom" || got.Selection.Effort != "" {
		t.Fatalf("manual model: %+v %v", got, err)
	}
}

func TestDelegationPluginDiscoveryUsesRegisteredCapability(t *testing.T) {
	d := newDaemonForTest(t)
	configuredBuild(t, d)
	client, done := startPluginPipe(t, d, "catalog-fixture", nil)
	defer func() { _ = client.Close(); <-done }()
	registerTestPluginDriver(t, client, "fixture", map[string]bool{"initial_prompt": true, "model_pin": true, "effort_pin": true, "model_discovery": true})
	found := false
	for _, h := range d.delegationHarnesses() {
		if h.ID == "fixture" {
			found = h.Discovery
		}
	}
	if !found {
		t.Fatal("plugin discovery capability missing")
	}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		for {
			request := decodeJSONRPCMessage(t, client)
			if request.Method == pluginHealthMethod {
				respondPluginRequest(t, client, request, pluginHealthResult{OK: true})
				continue
			}
			if request.Method != "driver.models" {
				t.Errorf("unexpected method %s", request.Method)
				return
			}
			respondPluginRequest(t, client, request, delegationModelCatalog{Models: []protocol.DelegationModel{{Harness: "fixture", Provider: "work", ID: "custom", EffortSupport: protocol.ModelCapabilitySupportSupported, EffortLevels: []string{"low", "high"}}}})
			return
		}
	}()
	catalog, err := d.discoverDelegationModels(context.Background(), "fixture")
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].Provider != "work" || catalog.Models[0].Access != "unknown" {
		t.Fatalf("%+v %v", catalog, err)
	}
	<-returned
}

func TestCrewLaunchValidationUsesProviderQualifiedModelIdentity(t *testing.T) {
	d := newDaemonForTest(t)
	client, done := startPluginPipe(t, d, "crew-catalog-fixture", nil)
	defer func() { _ = client.Close(); <-done }()
	registerTestPluginDriver(t, client, "fixture", map[string]bool{
		"initial_prompt": true, "model_pin": true, "effort_pin": true, "model_discovery": true,
	})
	catalog := delegationModelCatalog{Models: []protocol.DelegationModel{
		{Harness: "fixture", Provider: "first", ID: "shared", Access: protocol.ModelCapabilitySupportSupported, EffortSupport: protocol.ModelCapabilitySupportSupported, EffortLevels: []string{"low"}},
		{Harness: "fixture", Provider: "second", ID: "shared", Access: protocol.ModelCapabilitySupportUnsupported, Detail: "second provider is unavailable"},
		{Harness: "fixture", Provider: "third", ID: "fixed", Access: protocol.ModelCapabilitySupportSupported, EffortSupport: protocol.ModelCapabilitySupportUnsupported},
	}}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		models := 0
		for models < 3 {
			request := decodeJSONRPCMessage(t, client)
			if request.Method == pluginHealthMethod {
				respondPluginRequest(t, client, request, pluginHealthResult{OK: true})
				continue
			}
			if request.Method != "driver.models" {
				t.Errorf("unexpected method %s", request.Method)
				return
			}
			respondPluginRequest(t, client, request, catalog)
			models++
		}
	}()

	if err := d.validateCrewLaunchSelection(crew.Member{Agent: "fixture", Model: "first/shared", Effort: "low"}, true); err != nil {
		t.Fatalf("supported qualified model: %v", err)
	}
	if err := d.validateCrewLaunchSelection(crew.Member{Agent: "fixture", Model: "second/shared"}, true); err == nil || !strings.Contains(err.Error(), "second provider is unavailable") {
		t.Fatalf("unsupported provider-qualified model error = %v", err)
	}
	if err := d.validateCrewLaunchSelection(crew.Member{Agent: "fixture", Model: "third/fixed", Effort: "high"}, true); err == nil || !strings.Contains(err.Error(), "does not support effort") {
		t.Fatalf("unsupported qualified effort error = %v", err)
	}
	<-returned
}
