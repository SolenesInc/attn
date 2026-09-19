package automode

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
)

const (
	SlotList   = "list"
	SlotChoice = "choice"
)

// Slot is one question the Guardian's security policy asks about this machine.
// TestEverySlotIsReadByARuleThatExists fails on a slot no policy rule reads.
type Slot struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Choices []string `json:"choices,omitempty"`
	Detail  string   `json:"detail"`
	Unset   string   `json:"unset"`
	// Detected slots are filled from the session at launch; a user value wins.
	Detected bool     `json:"detected,omitempty"`
	ReadBy   []string `json:"read_by"`
}

// Slots is the environment's whole schema, in render order. environment.ts
// mirrors it; both sides pin the same ordered ids so neither moves alone.
func Slots() []Slot {
	return []Slot{
		{
			ID: "trusted_repo", Label: "Trusted repo", Kind: SlotList, Detected: true,
			Detail: "The repository this session works in, and its remotes.",
			Unset:  prompts.RenderText("pi-environment", "unset-trusted_repo", prompts.Values{}),
			ReadBy: []string{"Data Exfiltration", "Destructive Actions"},
		},
		{
			ID: "repo_visibility", Label: "Repository visibility", Kind: SlotChoice, Detected: true,
			Choices: []string{"private", "public"},
			Detail:  "Whether that repository is private or public.",
			Unset:   prompts.RenderText("pi-environment", "unset-repo_visibility", prompts.Values{}),
			ReadBy:  []string{"Data Exfiltration"},
		},
		{
			ID: "domains", Label: "Trusted internal domains", Kind: SlotList,
			Detail: "Hosts the agent may send data to. One per entry.",
			Unset:  prompts.RenderText("pi-environment", "unset-domains", prompts.Values{}),
			ReadBy: []string{"Data Exfiltration"},
		},
		{
			ID: "buckets", Label: "Trusted cloud buckets", Kind: SlotList,
			Detail: "Object storage that belongs to this work, such as s3://acme-artifacts.",
			Unset:  prompts.RenderText("pi-environment", "unset-buckets", prompts.Values{}),
			ReadBy: []string{"Data Exfiltration"},
		},
		{
			ID: "services", Label: "Key internal services", Kind: SlotList,
			Detail: "Named services the agent may talk to in the normal way.",
			Unset:  prompts.RenderText("pi-environment", "unset-services", prompts.Values{}),
			ReadBy: []string{"Data Exfiltration"},
		},
		{
			ID: "source_control", Label: "Source-control orgs", Kind: SlotList,
			Detail: "Orgs whose code may be pulled in and run.",
			Unset:  prompts.RenderText("pi-environment", "unset-source_control", prompts.Values{}),
			ReadBy: []string{"Persistent Security Weakening"},
		},
		{
			ID: "registry", Label: "Internal package registry", Kind: SlotList,
			Detail: "An internal registry or mirror, when you have one.",
			Unset:  prompts.RenderText("pi-environment", "unset-registry", prompts.Values{}),
			ReadBy: []string{"Persistent Security Weakening"},
		},
		{
			ID: "sensitive_data", Label: "Sensitive data locations", Kind: SlotList,
			Detail: "Where personal, customer or regulated data lives.",
			Unset:  prompts.RenderText("pi-environment", "unset-sensitive_data", prompts.Values{}),
			ReadBy: []string{"Data Exfiltration"},
		},
		{
			ID: "audiences", Label: "Cleared audiences", Kind: SlotList,
			Detail: "Who may see data read from those locations.",
			Unset:  prompts.RenderText("pi-environment", "unset-audiences", prompts.Values{}),
			ReadBy: []string{"Data Exfiltration"},
		},
		{
			ID: "remote_targets", Label: "Sensitive remote targets", Kind: SlotList,
			Detail: "Namespaces, hosts or workloads that are live.",
			Unset:  prompts.RenderText("pi-environment", "unset-remote_targets", prompts.Values{}),
			ReadBy: []string{"Destructive Actions"},
		},
		{
			ID: "iac_scopes", Label: "Protected IaC scopes", Kind: SlotList,
			Detail: "Infrastructure whose apply or destroy needs a person.",
			Unset:  prompts.RenderText("pi-environment", "unset-iac_scopes", prompts.Values{}),
			ReadBy: []string{"Destructive Actions"},
		},
	}
}

func FindSlot(id string) (Slot, bool) {
	for _, slot := range Slots() {
		if slot.ID == id {
			return slot, true
		}
	}
	return Slot{}, false
}

func SlotIDs() []string {
	slots := Slots()
	ids := make([]string, 0, len(slots))
	for _, slot := range slots {
		ids = append(ids, slot.ID)
	}
	return ids
}

type Environment struct {
	Slots map[string][]string `json:"slots"`
	Notes []string            `json:"notes"`
}

func NewEnvironment() Environment {
	return Environment{Slots: map[string][]string{}, Notes: []string{}}
}

func (e *Environment) SetSlot(id string, values []string) error {
	slot, ok := FindSlot(id)
	if !ok {
		return fmt.Errorf("no environment slot %q (want one of %s)", id, strings.Join(SlotIDs(), ", "))
	}
	cleaned := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		if slot.Kind == SlotChoice && !contains(slot.Choices, value) {
			return fmt.Errorf("environment slot %s takes one of %s, not %q",
				id, strings.Join(slot.Choices, ", "), value)
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	if slot.Kind == SlotChoice && len(cleaned) > 1 {
		return fmt.Errorf("environment slot %s holds one value, got %d", id, len(cleaned))
	}
	if e.Slots == nil {
		e.Slots = map[string][]string{}
	}
	if len(cleaned) == 0 {
		delete(e.Slots, id)
		return nil
	}
	e.Slots[id] = cleaned
	return nil
}

func (e Environment) Filled() (int, int) {
	filled := 0
	for _, id := range SlotIDs() {
		if len(e.Slots[id]) > 0 {
			filled++
		}
	}
	return filled, len(Slots())
}

func (e Environment) Normalize() Environment {
	out := NewEnvironment()
	for _, id := range SlotIDs() {
		if values := e.Slots[id]; len(values) > 0 {
			out.Slots[id] = append([]string{}, values...)
		}
	}
	out.Notes = append([]string{}, e.Notes...)
	return out
}

func (e Environment) UnknownSlots() []string {
	unknown := []string{}
	for id := range e.Slots {
		if _, ok := FindSlot(id); !ok {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (e Environment) WithDetected(detected map[string][]string) Environment {
	out := e.Normalize()
	if len(detected) == 0 {
		return out
	}
	for _, slot := range Slots() {
		if !slot.Detected || len(out.Slots[slot.ID]) > 0 {
			continue
		}
		values := []string{}
		seen := map[string]bool{}
		for _, value := range detected[slot.ID] {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			if slot.Kind == SlotChoice && !contains(slot.Choices, value) {
				continue
			}
			seen[value] = true
			values = append(values, value)
		}
		if slot.Kind == SlotChoice && len(values) > 1 {
			values = values[:1]
		}
		if len(values) > 0 {
			out.Slots[slot.ID] = values
		}
	}
	return out
}

func DetectedSlotIDs() []string {
	ids := []string{}
	for _, slot := range Slots() {
		if slot.Detected {
			ids = append(ids, slot.ID)
		}
	}
	return ids
}
