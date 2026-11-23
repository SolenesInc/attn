package modeltiers

import "strings"

type Tier string

const (
	Light    Tier = "light"
	Standard Tier = "standard"
	Deep     Tier = "deep"
)

type Rule struct {
	Match string
	Tier  Tier
	Alias bool
}

var Shipped = []Rule{
	{Match: "default", Alias: true},
	{Match: "opus", Tier: Deep},
	{Match: "fable", Tier: Deep},
	{Match: "claude-opus-*", Tier: Deep},
	{Match: "claude-fable-*", Tier: Deep},
	{Match: "*-sol", Tier: Deep},
	{Match: "*-astra", Tier: Deep},
	{Match: "sonnet", Tier: Standard},
	{Match: "claude-sonnet-*", Tier: Standard},
	{Match: "*-terra", Tier: Standard},
	{Match: "haiku", Tier: Light},
	{Match: "claude-haiku-*", Tier: Light},
	{Match: "*-luna", Tier: Light},
}

type Key struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
}

type Override struct {
	Key
	Tier Tier `json:"tier"`
}

type Overrides map[Key]Tier

type Assignment struct {
	Tier    Tier
	Source  string
	Shipped Tier
}

func Assign(overrides Overrides, key Key, family string) Assignment {
	assignment := Assignment{Source: "none"}
	if family == "" {
		family = key.Model
	}
	for _, rule := range Shipped {
		prefix, suffix, wildcard := strings.Cut(rule.Match, "*")
		matches := family == rule.Match
		if wildcard {
			matches = strings.HasPrefix(family, prefix) && strings.HasSuffix(family, suffix)
		}
		if !matches {
			continue
		}
		if rule.Alias {
			return Assignment{Source: "alias"}
		}
		assignment = Assignment{Tier: rule.Tier, Source: "shipped", Shipped: rule.Tier}
		break
	}
	if tier, ok := overrides[key]; ok {
		assignment.Tier = tier
		assignment.Source = "override"
	}
	return assignment
}
