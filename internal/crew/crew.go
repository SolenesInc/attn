package crew

import (
	"encoding/json"
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
)

const (
	Namespace         = "core/crew"
	CollectionMembers = "members"
)

const HomesDirName = "crew"

const CharterFileName = "CHARTER.md"

const DefaultAgent = "claude"

type Member struct {
	ID             string             `json:"id"`
	CWD            string             `json:"cwd"`
	Agent          string             `json:"agent"`
	Model          string             `json:"model"`
	Effort         string             `json:"effort"`
	AwarenessDirs  []string           `json:"awareness_dirs"`
	BindingSession protocol.SessionID `json:"binding_session"`

	LetterFile      string                `json:"letter_file"`
	LetterSession   protocol.SessionID    `json:"letter_session"`
	LetterDay       string                `json:"letter_day,omitempty"`
	LetterClose     protocol.CrewDayClose `json:"letter_close,omitempty"`
	AutonomousWakes []string              `json:"autonomous_wakes"`
}

func (m Member) HomeDir(dataRoot string) string {
	return filepath.Join(dataRoot, HomesDirName, m.ID)
}

func (m Member) CharterPath(dataRoot string) string {
	return filepath.Join(m.HomeDir(dataRoot), CharterFileName)
}

func (m Member) LaunchAgent() string {
	if agent := strings.TrimSpace(strings.ToLower(m.Agent)); agent != "" {
		return agent
	}
	return DefaultAgent
}

func (m Member) FiledLetterFor(sessionID protocol.SessionID) (string, bool) {
	if sessionID == "" || m.LetterFile == "" || m.LetterSession != sessionID {
		return "", false
	}
	return m.LetterFile, true
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

func (m Member) Encode() ([]byte, error) {
	if m.AwarenessDirs == nil {
		m.AwarenessDirs = []string{}
	}
	if m.AutonomousWakes == nil {
		m.AutonomousWakes = []string{}
	}
	return json.Marshal(m)
}

func Decode(body []byte) (Member, error) {
	var member Member
	if err := json.Unmarshal(body, &member); err != nil {
		return Member{}, fmt.Errorf("this member's stored record is not readable: %w", err)
	}
	return member, nil
}

const MaxIDChars = 40

var memberIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

const DaemonID = "attn"

func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("a member id is required")
	}
	if len(id) > MaxIDChars {
		return fmt.Errorf("%q is %d characters and a member id's limit is %d — a member's name is said out loud", id, len(id), MaxIDChars)
	}
	if !memberIDRe.MatchString(id) {
		return fmt.Errorf("%q is not a member id: lowercase letters, digits and - only, starting with a letter, like `trellis`", id)
	}
	if id == DaemonID {
		return fmt.Errorf("%q is the name attn itself moves seeds under; pick another id for a crew member", id)
	}
	return docstore.ValidateDocumentID(id)
}

func DisplayName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if id == DaemonID {
		return id
	}
	first, size := utf8.DecodeRuneInString(id)
	return string(unicode.ToUpper(first)) + id[size:]
}

func HolderName(member string, session protocol.SessionID) string {
	if strings.TrimSpace(member) != "" {
		return DisplayName(member)
	}
	return string(protocol.TrimID(session))
}

func Resolve(name string, members []Member) (Member, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Member{}, false
	}
	for _, m := range members {
		if strings.EqualFold(m.ID, name) {
			return m, true
		}
	}
	return Member{}, false
}

func ScanHomes(dir string, warn func(format string, args ...any)) ([]Member, error) {
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
	var members []Member
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		home := filepath.Join(dir, id)
		charter := filepath.Join(home, CharterFileName)
		if _, err := os.Stat(charter); err != nil {
			continue
		}
		if err := ValidateID(id); err != nil {
			warn("crew: skipping home %s: %v", home, err)
			continue
		}
		members = append(members, Member{
			ID:            id,
			AwarenessDirs: []string{},
		})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members, nil
}
