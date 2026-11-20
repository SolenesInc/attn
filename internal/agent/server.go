package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

const ServerProtocol = "attn-app-server-v1"

type ServerDeclaration struct {
	Protocol                 string          `json:"protocol"`
	Executable               string          `json:"executable"`
	ExecutableEnv            string          `json:"executable_env,omitempty"`
	ServeArgs                []string        `json:"serve_args"`
	HomeEnv                  string          `json:"home_env"`
	DefaultHome              string          `json:"default_home"`
	SocketName               string          `json:"socket_name"`
	Initialize               ServerHandshake `json:"initialize"`
	CallerConversationEnv    string          `json:"caller_conversation_env"`
	CallerConversationHeader string          `json:"caller_conversation_header,omitempty"`
	OnClose                  string          `json:"on_close"`
	Recovery                 string          `json:"recovery"`
	Targets                  bool            `json:"targets,omitempty"`
	SigmaWire                bool            `json:"-"`
	ConversationWatch        bool            `json:"conversation_watch,omitempty"`
}

type ServerHandshake struct {
	Server   string                `json:"server,omitempty"`
	Protocol ServerProtocolVersion `json:"protocol"`
}

type ServerProtocolVersion struct {
	Exact int `json:"exact,omitempty"`
	Min   int `json:"min,omitempty"`
}

func (v ServerProtocolVersion) Requested() int {
	if v.Exact > 0 {
		return v.Exact
	}
	return v.Min
}

type ServerDeclarer interface {
	Server() ServerDeclaration
}

func DeclaredServer(d Driver) (ServerDeclaration, bool) {
	declarer, ok := d.(ServerDeclarer)
	if !ok {
		return ServerDeclaration{}, false
	}
	return declarer.Server(), true
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

func (p *ServerDeclaration) Validate() error {
	if p.Protocol != ServerProtocol {
		return fmt.Errorf("unsupported server binding protocol %q; need %s", p.Protocol, ServerProtocol)
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
	v := p.Initialize.Protocol
	if (v.Exact > 0) == (v.Min > 0) || v.Exact < 0 || v.Min < 0 {
		return fmt.Errorf("server initialize.protocol requires either a positive exact or min version")
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
