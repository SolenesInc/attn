package bridge

import (
	"fmt"
)

type Identity struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	Instance string `json:"instance"`
	Build    Build  `json:"build"`
}

type Build struct {
	Version           string `json:"version"`
	SourceFingerprint string `json:"source_fingerprint"`
	ProtocolVersion   string `json:"protocol_version"`
}

func (b Build) String() string {
	return fmt.Sprintf("%s (source %s, protocol %s)", b.Version, b.SourceFingerprint, b.ProtocolVersion)
}

type Refusal struct {
	Peer   *Identity `json:"-"`
	Reason string    `json:"reason"`
	Detail string    `json:"detail"`
}

func (r *Refusal) Error() string { return r.Detail }

type hello struct {
	Bridge int `json:"bridge"`
	Identity
	Refused *Refusal `json:"refused,omitempty"`
}

func greeting(self Identity) hello { return hello{Bridge: 1, Identity: self} }

func (h hello) check() error {
	if h.Bridge != 1 || h.DaemonID == "" {
		return fmt.Errorf("invalid bridge hello: bridge=%d, daemon_id=%q", h.Bridge, h.DaemonID)
	}
	return nil
}

func buildRefusal(self, peer Identity) *Refusal {
	if self.Build == peer.Build {
		return nil
	}
	return &Refusal{Reason: "build", Detail: fmt.Sprintf("daemon %s runs build %s; daemon %s runs build %s", self.DaemonID, self.Build, peer.DaemonID, peer.Build)}
}
