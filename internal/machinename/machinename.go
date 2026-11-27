// Package machinename reads a machine's name as a person or an agent wrote it.
package machinename

import (
	"fmt"
	"slices"
	"strings"
)

// HomeName names the home, wherever it runs.
const HomeName = "home"

// Machine is one machine a name can read as. Home marks the home's own machine.
type Machine struct {
	ID   string
	Name string
	Home bool
}

// Unknown is a name no machine holds. Names are the names that do.
type Unknown struct {
	Text  string
	Names []string
}

func (e *Unknown) Error() string {
	return fmt.Sprintf("no machine %q; machines: %s", e.Text, strings.Join(e.Names, ", "))
}

// Shared is a name that more than one machine holds. IDs are theirs, in the order the list gave them.
type Shared struct {
	Text string
	IDs  []string
}

func (e *Shared) Error() string {
	return fmt.Sprintf("%q names more than one machine: %s; name one by its id", e.Text, strings.Join(e.IDs, ", "))
}

// Read returns the machine whose name or id is text, ignoring case. `home` names the home.
func Read(text string, machines []Machine) (Machine, error) {
	text = strings.TrimSpace(text)
	var found []Machine
	for _, m := range machines {
		if holds(m, text) && !slices.ContainsFunc(found, func(f Machine) bool { return f.ID == m.ID }) {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Machine{}, &Unknown{Text: text, Names: names(machines)}
	}
	ids := make([]string, len(found))
	for i, m := range found {
		ids[i] = m.ID
	}
	return Machine{}, &Shared{Text: text, IDs: ids}
}

func holds(m Machine, text string) bool {
	return m.ID == text || m.Name != "" && strings.EqualFold(m.Name, text) || m.Home && strings.EqualFold(HomeName, text)
}

func names(machines []Machine) []string {
	out := []string{HomeName}
	for _, m := range machines {
		if m.Name != "" && !slices.ContainsFunc(out, func(n string) bool { return strings.EqualFold(n, m.Name) }) {
			out = append(out, m.Name)
		}
	}
	return out
}

// Address is text that names a machine and a place on it, as ssh writes it.
type Address struct {
	Machine Machine
	Rest    string
}

// schemes start text that is a URL, never an address.
var schemes = []string{"http", "https", "mailto", "attn", "file"}

// ReadAddress splits `<machine>:<rest>` where rest is an absolute or `~` path. The longest machine name that
// starts text wins, so names that hold `:` work as written. ok is false when text is not written as an address,
// such as a bare path, a relative rest or a URL; err says why the machine it names is not one.
func ReadAddress(text string, machines []Machine) (address Address, ok bool, err error) {
	lower := strings.ToLower(text)
	if slices.ContainsFunc(schemes, func(s string) bool { return strings.HasPrefix(lower, s+":") }) {
		return Address{}, false, nil
	}
	name := longestLabel(text, machines)
	if name == "" {
		name, _, _ = strings.Cut(text, ":")
		if name == "" || name == text || strings.Contains(name, "/") {
			return Address{}, false, nil
		}
	}
	rest := text[len(name)+1:]
	if !isPath(rest) {
		return Address{}, false, nil
	}
	m, err := Read(text[:len(name)], machines)
	if err != nil {
		return Address{}, true, err
	}
	return Address{Machine: m, Rest: rest}, true, nil
}

func longestLabel(text string, machines []Machine) string {
	longest := ""
	for _, m := range machines {
		for _, label := range []string{m.ID, m.Name, homeLabel(m)} {
			if label != "" && len(label) > len(longest) && len(text) > len(label) && text[len(label)] == ':' && strings.EqualFold(text[:len(label)], label) {
				longest = label
			}
		}
	}
	return longest
}

func homeLabel(m Machine) string {
	if m.Home {
		return HomeName
	}
	return ""
}

func isPath(rest string) bool {
	return strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "~")
}
