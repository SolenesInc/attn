package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

const Surface = "the crew"

const (
	Namespace                 = "core/crew"
	CollectionMembers         = "members"
	CollectionRestartRequests = "restart_requests"
)

const HomesDirName = "crew"

const CharterFileName = "CHARTER.md"

const DefaultAgent = "claude"

type Member struct {
	Key            who.MemberKey      `json:"-"`
	CharterPath    string             `json:"charter_path"`
	HomeDir        string             `json:"home_dir"`
	CWD            string             `json:"cwd"`
	Agent          string             `json:"agent"`
	Model          string             `json:"model"`
	Effort         string             `json:"effort"`
	AwarenessDirs  []string           `json:"awareness_dirs"`
	BindingSession protocol.SessionID `json:"binding_session"`

	LetterPath      string             `json:"letter_path"`
	LetterSession   protocol.SessionID `json:"letter_session"`
	AutonomousWakes []string           `json:"autonomous_wakes"`
	Restart         *Restart           `json:"restart,omitempty"`
}

type RestartState string

const (
	RestartQueued    RestartState = "queued"
	RestartRequested RestartState = "requested"
	RestartFailed    RestartState = "failed"
	RestartCompleted RestartState = "completed"
)

type Restart struct {
	RequestID          string             `json:"request_id"`
	SessionID          protocol.SessionID `json:"session_id"`
	State              RestartState       `json:"state"`
	DeliveryStatus     string             `json:"delivery_status,omitempty"`
	Detail             string             `json:"detail,omitempty"`
	Error              string             `json:"error,omitempty"`
	LetterPath         string             `json:"letter_path,omitempty"`
	SuccessorSessionID protocol.SessionID `json:"successor_session_id,omitempty"`
	Withdrawn          bool               `json:"withdrawn,omitempty"`
}

type RestartRequest struct {
	Member           who.MemberKey `json:"member"`
	RequestID        string        `json:"request_id"`
	RestartRequestID string        `json:"restart_request_id"`
}

func (m Member) LaunchAgent() string {
	if agent := strings.TrimSpace(strings.ToLower(m.Agent)); agent != "" {
		return agent
	}
	return DefaultAgent
}

func (m Member) FiledLetterFor(sessionID protocol.SessionID) (string, bool) {
	if sessionID == "" || m.LetterPath == "" || m.LetterSession != sessionID {
		return "", false
	}
	return m.LetterPath, true
}

func MembersSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionMembers,
		Fields: []docstore.FieldSpec{
			{Name: "binding_session", Type: docstore.FieldString},
		},
	}
}

func RestartRequestsSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{Namespace: Namespace, Collection: CollectionRestartRequests}
}

func (r RestartRequest) Encode() ([]byte, error) {
	return json.Marshal(r)
}

func DecodeRestartRequest(body []byte) (RestartRequest, error) {
	var request RestartRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return RestartRequest{}, fmt.Errorf("this restart request's stored record is not readable: %w", err)
	}
	return request, nil
}

func (m Member) Encode() ([]byte, error) {
	if m.AwarenessDirs == nil {
		m.AwarenessDirs = []string{}
	}
	if m.AutonomousWakes == nil {
		m.AutonomousWakes = []string{}
	}
	return json.Marshal(m)
}

func Decode(id string, body []byte) (Member, error) {
	var member Member
	if err := json.Unmarshal(body, &member); err != nil {
		return Member{}, fmt.Errorf("this member's stored record is not readable: %w", err)
	}
	key, err := who.ParseMemberKey(id)
	if err != nil {
		return Member{}, err
	}
	member.Key = key
	return member, nil
}

const DaemonID = "attn"

var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,39}$`)
var idNamePattern = regexp.MustCompile(`^[A-Za-z]-[A-Za-z0-9]{6}$`)
var ErrNameReservedForChief = errors.New("chief is reserved for the profile's chief")

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "chief") {
		return ErrNameReservedForChief
	}
	if name == "" {
		return errors.New("a crew member name is required")
	}
	if len(name) > 40 {
		return fmt.Errorf("name limit is 40 characters, asked for %d", len(name))
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%q is not a member name: letters, digits and - only, starting with a letter", name)
	}
	switch strings.ToLower(name) {
	case "attn", "user", "you":
		return fmt.Errorf("%q names who acts, not a crew member", name)
	}
	if idNamePattern.MatchString(name) {
		return fmt.Errorf("%q reads as an id; choose a member name", name)
	}
	return nil
}
func NameFromKey(k who.MemberKey) string {
	name := KeyLabel(k)
	if ValidateName(name) != nil {
		return k.String() + "-crew"
	}
	return name
}
func KeyLabel(k who.MemberKey) string {
	id := k.String()
	if id == "" || id == DaemonID {
		return id
	}
	first, size := utf8.DecodeRuneInString(id)
	return string(unicode.ToUpper(first)) + id[size:]
}
func HolderName(member string, session protocol.SessionID) string {
	if strings.TrimSpace(member) != "" {
		k, err := who.ParseMemberKey(member)
		if err != nil {
			return strings.TrimSpace(member)
		}
		return KeyLabel(k)
	}
	return string(protocol.TrimID(session))
}

type Home struct {
	Member    Member
	ProfileID string
}

func ScanHomes(dir string, warn func(string, ...any)) ([]Home, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading crew homes at %s: %w", dir, err)
	}
	if warn == nil {
		warn = func(string, ...any) {}
	}
	var homes []Home
	add := func(home, profile string) bool {
		charter := filepath.Join(home, CharterFileName)
		if _, err := os.Stat(charter); err != nil {
			return false
		}
		key, err := who.MemberKeyOfHome(home)
		if err != nil {
			warn("crew: skipping home %s: %v", home, err)
			return true
		}
		homes = append(homes, Home{Member: Member{Key: key, HomeDir: home, CharterPath: charter, AwarenessDirs: []string{}}, ProfileID: profile})
		return true
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		home := filepath.Join(dir, entry.Name())
		if add(home, "") {
			continue
		}
		children, err := os.ReadDir(home)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.IsDir() {
				add(filepath.Join(home, child.Name()), entry.Name())
			}
		}
	}
	sort.Slice(homes, func(i, j int) bool { return homes[i].Member.Key.String() < homes[j].Member.Key.String() })
	return homes, nil
}
