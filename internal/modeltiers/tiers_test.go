package modeltiers_test

import (
	"testing"

	"github.com/victorarias/attn/internal/modeltiers"
)

func TestModelIDRulesAssignTheSameTierOnEveryHarness(t *testing.T) {
	for _, row := range []struct {
		model  string
		tier   modeltiers.Tier
		source string
	}{
		{"default", "", "alias"},
		{"opus", modeltiers.Deep, "shipped"},
		{"fable", modeltiers.Deep, "shipped"},
		{"claude-opus-5", modeltiers.Deep, "shipped"},
		{"claude-fable-5", modeltiers.Deep, "shipped"},
		{"gpt-6.1-sol", modeltiers.Deep, "shipped"},
		{"gpt-6-astra", modeltiers.Deep, "shipped"},
		{"sonnet", modeltiers.Standard, "shipped"},
		{"claude-sonnet-5", modeltiers.Standard, "shipped"},
		{"gpt-5.6-terra", modeltiers.Standard, "shipped"},
		{"haiku", modeltiers.Light, "shipped"},
		{"claude-haiku-4-5", modeltiers.Light, "shipped"},
		{"gpt-6-luna", modeltiers.Light, "shipped"},
		{"claude-haiku-future-sol", modeltiers.Deep, "shipped"},
		{"gemini-3-flash", "", "none"},
		{"gemini-3-pro", "", "none"},
		{"qwen3-coder:32b", "", "none"},
		{"opus-extra", "", "none"},
	} {
		for _, harness := range []string{"claude", "codex", "copilot", "pi"} {
			t.Run(harness+"/"+row.model, func(t *testing.T) {
				got := modeltiers.Assign(nil, modeltiers.Key{Harness: harness, Provider: "provider", Model: row.model})
				want := modeltiers.Assignment{Tier: row.tier, Source: row.source, Shipped: row.tier}
				if got != want {
					t.Errorf("assignment = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestOverridesBelongToOneHarnessProviderAndModelAndLeaveAliasesUntiered(t *testing.T) {
	key := modeltiers.Key{Harness: "pi", Provider: "openai", Model: "gpt-6-luna"}
	alias := modeltiers.Key{Harness: "claude", Model: "default"}
	overrides := modeltiers.Overrides{key: modeltiers.Deep, alias: modeltiers.Deep}
	for _, row := range []struct {
		key  modeltiers.Key
		want modeltiers.Assignment
	}{
		{key, modeltiers.Assignment{Tier: modeltiers.Deep, Source: "override", Shipped: modeltiers.Light}},
		{modeltiers.Key{Harness: "codex", Provider: "openai", Model: key.Model}, modeltiers.Assignment{Tier: modeltiers.Light, Source: "shipped", Shipped: modeltiers.Light}},
		{modeltiers.Key{Harness: "pi", Provider: "other", Model: key.Model}, modeltiers.Assignment{Tier: modeltiers.Light, Source: "shipped", Shipped: modeltiers.Light}},
		{modeltiers.Key{Harness: "pi", Provider: "openai", Model: "gpt-5.6-luna"}, modeltiers.Assignment{Tier: modeltiers.Light, Source: "shipped", Shipped: modeltiers.Light}},
		{alias, modeltiers.Assignment{Source: "alias"}},
	} {
		if got := modeltiers.Assign(overrides, row.key); got != row.want {
			t.Errorf("assignment for %+v = %+v, want %+v", row.key, got, row.want)
		}
	}
	delete(overrides, key)
	if got := modeltiers.Assign(overrides, key); got.Tier != modeltiers.Light || got.Source != "shipped" {
		t.Errorf("removing override assigned %+v, want the shipped light tier", got)
	}
}
