package machinename

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
)

type table struct {
	Machines []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Home bool   `json:"home"`
	} `json:"machines"`
	Names     []nameCase `json:"names"`
	Addresses []nameCase `json:"addresses"`
}

type nameCase struct {
	Text    string   `json:"text"`
	Machine string   `json:"machine"`
	Rest    string   `json:"rest"`
	Shared  []string `json:"shared"`
	Unknown bool     `json:"unknown"`
	Plain   bool     `json:"plain"`
}

func loadTable(t *testing.T) (table, []Machine) {
	raw, err := os.ReadFile("testdata/names.json")
	if err != nil {
		t.Fatal(err)
	}
	var tab table
	if err := json.Unmarshal(raw, &tab); err != nil {
		t.Fatal(err)
	}
	machines := make([]Machine, len(tab.Machines))
	for i, m := range tab.Machines {
		machines[i] = Machine{ID: m.ID, Name: m.Name, Home: m.Home}
	}
	return tab, machines
}

func checkOutcome(t *testing.T, c nameCase, got Machine, err error) {
	t.Helper()
	var unknown *Unknown
	var shared *Shared
	switch {
	case c.Unknown:
		if !errors.As(err, &unknown) {
			t.Fatalf("%q: got %v, %v; want an unknown machine", c.Text, got, err)
		}
		if !slices.Contains(unknown.Names, "home") || !slices.Contains(unknown.Names, "Studio") {
			t.Fatalf("%q: the refusal names %v; want every machine", c.Text, unknown.Names)
		}
	case c.Shared != nil:
		if !errors.As(err, &shared) || !slices.Equal(shared.IDs, c.Shared) {
			t.Fatalf("%q: got %v, %v; want shared by %v", c.Text, got, err, c.Shared)
		}
	default:
		if err != nil || got.ID != c.Machine {
			t.Fatalf("%q: got %v, %v; want %s", c.Text, got, err, c.Machine)
		}
	}
}

func TestNames(t *testing.T) {
	tab, machines := loadTable(t)
	for _, c := range tab.Names {
		got, err := Read(c.Text, machines)
		checkOutcome(t, c, got, err)
	}
}

func TestAddresses(t *testing.T) {
	tab, machines := loadTable(t)
	for _, c := range tab.Addresses {
		got, ok, err := ReadAddress(c.Text, machines)
		if c.Plain {
			if ok || err != nil {
				t.Fatalf("%q: read as an address on %v (%v); want plain text", c.Text, got.Machine, err)
			}
			continue
		}
		if !ok {
			t.Fatalf("%q: not read as an address", c.Text)
		}
		checkOutcome(t, c, got.Machine, err)
		if err == nil && got.Rest != c.Rest {
			t.Fatalf("%q: rest %q, want %q", c.Text, got.Rest, c.Rest)
		}
	}
}
