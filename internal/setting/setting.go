package setting

import (
	"fmt"
	"strings"
)

type Class int

const (
	Home Class = iota + 1
	Machine
)

type Scope int

const (
	AllProfiles Scope = iota
	PerProfile
)

type Kind int

const (
	Bool Kind = iota + 1
	Free
	Checked
	Computed
)

type Default int

const (
	Off Default = iota
	On
)

type Stage int

const (
	NotAFeature Stage = iota
	Experimental
	OnByDefault
)

func (s Stage) String() string {
	switch s {
	case Experimental:
		return "experimental"
	case OnByDefault:
		return "on by default"
	default:
		return ""
	}
}

type Group string

const (
	Agents       Group = "Agents"
	Interface    Group = "Interface"
	Connections  Group = "Connections"
	Data         Group = "Data"
	UnderTheHood Group = "Under the hood"
)

type Applies string

const (
	Now             Applies = "Now"
	NewAgents       Applies = "New agents"
	NewAgentsOnOnly Applies = "On: new agents. Off: now"
	NewTerminals    Applies = "New terminals"
	NextDaemonStart Applies = "Next daemon start"
)

func (a Applies) Timing() string {
	switch a {
	case NewAgents:
		return "Starts with the next agent you open."
	case NewAgentsOnOnly:
		return "Starts with the next agent you open. Turning it off applies at once."
	case NewTerminals:
		return "Starts with the next terminal you open."
	case NextDaemonStart:
		return "Starts the next time attn's daemon restarts."
	}
	return ""
}

type Info struct {
	Name    string
	Summary string
	Group   Group
	Applies Applies
	Stage   Stage
	Since   string
	Search  string
}

type Setting struct {
	Info
	Key         string
	Class       Class
	Kind        Kind
	Scope       Scope
	Default     Default
	Internal    bool
	Description string
}

var (
	all        []*Setting
	exact      = map[string]*Setting{}
	prefixLed  []*Setting
	patternLed []*Setting
)

func add(s *Setting) *Setting {
	if !s.Internal {
		all = append(all, s)
	}
	exact[s.Key] = s
	switch {
	case !s.IsFamily():
	case strings.HasPrefix(s.Key, "<"):
		patternLed = append(patternLed, s)
	default:
		prefixLed = append(prefixLed, s)
	}
	return s
}

func feature(key, description string, info Info) *Setting {
	s := &Setting{Info: info, Key: key, Class: Home, Kind: Bool, Description: description}
	if info.Stage == OnByDefault {
		s.Default = On
	}
	return add(s)
}

func All() []*Setting { return all }

func Features() []*Setting {
	var features []*Setting
	for _, s := range all {
		if s.Stage != NotAFeature {
			features = append(features, s)
		}
	}
	return features
}

func (s *Setting) Refusal() error {
	return fmt.Errorf("%s is off. Turn it on in Settings → Experimental → %s (%s)", s.Name, s.Name, s.Key)
}

func (s *Setting) String() string { return s.Key }

func (s *Setting) IsFamily() bool { return strings.Contains(s.Key, "<") }

func (s *Setting) ReadOnly() bool { return s.Kind == Computed }

func (s *Setting) For(name string) *Setting {
	concrete := *s
	start := strings.IndexByte(s.Key, '<')
	end := strings.IndexByte(s.Key, '>')
	concrete.Key = s.Key[:start] + name + s.Key[end+1:]
	return &concrete
}

func (s *Setting) Matches(key string) bool { return s.IsFamily() && patternMatches(s.Key, key) }

func (s *Setting) WithDefault(stored string) string {
	if s.Default == On && strings.TrimSpace(stored) == "" {
		return "true"
	}
	return stored
}

func Lookup(key string) (*Setting, bool) {
	if s, ok := exact[key]; ok {
		return s, true
	}
	for _, families := range [][]*Setting{prefixLed, patternLed} {
		for _, s := range families {
			if s.Matches(key) {
				return s, true
			}
		}
	}
	return nil, false
}

func ClassOf(key string) Class {
	if s, ok := Lookup(key); ok {
		return s.Class
	}
	return Home
}

func FillDefaults(values map[string]string) {
	for _, s := range all {
		if s.Default == On {
			values[s.Key] = s.WithDefault(values[s.Key])
		}
	}
}

func patternMatches(pattern, key string) bool {
	for {
		start := strings.IndexByte(pattern, '<')
		if start < 0 {
			return pattern == key
		}
		prefix := pattern[:start]
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		key = strings.TrimPrefix(key, prefix)
		end := strings.IndexByte(pattern[start:], '>') + start
		pattern = pattern[end+1:]
		next := strings.IndexByte(pattern, '<')
		suffix := pattern
		if next >= 0 {
			suffix = pattern[:next]
		}
		if suffix == "" {
			return key != ""
		}
		at := strings.Index(key, suffix)
		if at <= 0 {
			return false
		}
		key = key[at:]
	}
}
