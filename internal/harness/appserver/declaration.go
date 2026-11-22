package appserver

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const (
	Protocol        = "attn-app-server-v2"
	ProtocolVersion = 2
)

type Ability string

const (
	AbilityCompact      Ability = "compact"
	AbilityWatch        Ability = "watch"
	AbilityTargets      Ability = "targets"
	AbilitySteer        Ability = "steer"
	AbilityQuestionSafe Ability = "question_safe"
)

type Declaration struct {
	Protocol                 string    `json:"protocol"`
	Executable               string    `json:"executable"`
	ExecutableEnv            string    `json:"executable_env,omitempty"`
	ServeArgs                []string  `json:"serve_args"`
	HomeEnv                  string    `json:"home_env"`
	DefaultHome              string    `json:"default_home"`
	SocketName               string    `json:"socket_name"`
	Initialize               Handshake `json:"initialize,omitzero"`
	CallerConversationEnv    string    `json:"caller_conversation_env"`
	CallerConversationHeader string    `json:"caller_conversation_header,omitempty"`
	OnClose                  string    `json:"on_close"`
	Recovery                 string    `json:"recovery"`
	Abilities                []Ability `json:"abilities,omitempty"`
}

type Handshake struct {
	Server string `json:"server,omitempty"`
}

var knownAbilities = []Ability{AbilityCompact, AbilityWatch, AbilityTargets, AbilitySteer, AbilityQuestionSafe}

func (p *Declaration) UnknownAbilities() []Ability {
	var unknown []Ability
	for _, ability := range p.Abilities {
		if !slices.Contains(knownAbilities, ability) {
			unknown = append(unknown, ability)
		}
	}
	return unknown
}

func (p *Declaration) Can(ability Ability) bool {
	return p != nil && slices.Contains(p.Abilities, ability)
}

func ValidEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func StaysInside(relative string) bool {
	clean := filepath.Clean(relative)
	return !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func (p *Declaration) Validate() error {
	if p.Protocol != Protocol {
		return fmt.Errorf("unsupported server binding protocol %q; need %s", p.Protocol, Protocol)
	}
	if strings.TrimSpace(p.Executable) == "" {
		return fmt.Errorf("server executable is required")
	}
	if !ValidEnvironmentName(p.HomeEnv) || !ValidEnvironmentName(p.CallerConversationEnv) || (p.ExecutableEnv != "" && !ValidEnvironmentName(p.ExecutableEnv)) {
		return fmt.Errorf("server environment names must be valid variable names")
	}
	if !strings.HasSuffix(p.CallerConversationEnv, "_ROOT_CONVERSATION") || len(p.CallerConversationEnv) == len("_ROOT_CONVERSATION") {
		return fmt.Errorf("server caller_conversation_env must be named <NAME>_ROOT_CONVERSATION")
	}
	if p.CallerConversationHeader != "" && !validHeaderName(p.CallerConversationHeader) {
		return fmt.Errorf("server caller_conversation_header %q is not an HTTP header name", p.CallerConversationHeader)
	}
	if p.DefaultHome == "" || !StaysInside(p.DefaultHome) {
		return fmt.Errorf("server default_home must be relative to the user's home")
	}
	if p.SocketName == "" || filepath.Base(p.SocketName) != p.SocketName || p.SocketName == "." || p.SocketName == ".." {
		return fmt.Errorf("server socket_name must be a filename")
	}
	if p.OnClose != "interrupt" && p.OnClose != "leave-running" {
		return fmt.Errorf("unsupported server on_close %q", p.OnClose)
	}
	if p.Recovery != "last-run" && p.Recovery != "numbered-completions" {
		return fmt.Errorf("unsupported server recovery %q", p.Recovery)
	}
	return nil
}

func validHeaderName(name string) bool {
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return name != ""
}
