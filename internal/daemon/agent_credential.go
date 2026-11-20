package daemon

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const agentKeyFile = "agent-key"

type agentKey struct {
	secret []byte
	err    error
}

func loadAgentKey(dataRoot string) agentKey {
	path := filepath.Join(dataRoot, agentKeyFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = createAgentKey(dataRoot, path)
	}
	if err != nil {
		return agentKey{err: fmt.Errorf("agent key %s: %w", path, err)}
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(secret) < 32 {
		return agentKey{err: fmt.Errorf("agent key %s holds no 32-byte hex key", path)}
	}
	return agentKey{secret: secret}
}

func createAgentKey(dataRoot, path string) ([]byte, error) {
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return nil, err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	data := []byte(hex.EncodeToString(random))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return nil, err
	}
	return data, file.Close()
}

type agentCredentialKind byte

const (
	credentialTerminal agentCredentialKind = 't'
	credentialServer   agentCredentialKind = 's'
)

type agentCredential struct {
	kind agentCredentialKind
	name string
}

func (k agentKey) sign(c agentCredential) string {
	subject := string(c.kind) + "." + c.name
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(subject))
	return subject + "." + hex.EncodeToString(mac.Sum(nil))
}

func (k agentKey) verify(token string) (agentCredential, error) {
	if k.err != nil {
		return agentCredential{}, k.err
	}
	subject, signature, signed := cutLast(token, ".")
	kind, name, named := strings.Cut(subject, ".")
	c := agentCredential{name: name}
	if len(kind) == 1 {
		c.kind = agentCredentialKind(kind[0])
	}
	if !signed || !named || name == "" || (c.kind != credentialTerminal && c.kind != credentialServer) {
		return agentCredential{}, fmt.Errorf("the bearer token, %d bytes, is not an agent credential", len(token))
	}
	if want := k.sign(c); !hmac.Equal([]byte(want[len(subject)+1:]), []byte(signature)) {
		return agentCredential{}, fmt.Errorf("the bearer token for %s is not signed by this daemon's agent key", subject)
	}
	return c, nil
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

func (d *Daemon) agentToolsURL() string {
	return "http://" + net.JoinHostPort("127.0.0.1", config.WSPort()) + "/mcp"
}

func (d *Daemon) agentTools(t harness.TerminalID) *agentdriver.AgentTools {
	if !d.on(setting.AgentToolsEnabled) || d.agentKey.err != nil {
		return nil
	}
	return &agentdriver.AgentTools{URL: d.agentToolsURL(), Credential: d.agentKey.sign(agentCredential{kind: credentialTerminal, name: string(protocol.TrimID(protocol.TerminalID(t)))})}
}
