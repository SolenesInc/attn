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
)

const Surface = "the crew"

const (
	Namespace         = "core/crew"
	CollectionMembers = "members"
)

const HomesDirName = "crew"

const CharterFileName = "CHARTER.md"

// The crew simulation has run on Claude Code since 2026-08-06, so a member
// registered before the agent field existed keeps waking where it was.
const DefaultAgent = "claude"

// Every declared field is written unconditionally, empty string included, so a
// filter on `binding_session = ""` matches the members that are asleep.
type Member struct {
	ID             string   `json:"id"`
	CharterPath    string   `json:"charter_path"`
	HomeDir        string   `json:"home_dir"`
	CWD            string   `json:"cwd"`
	Agent          string   `json:"agent"`
	Model          string   `json:"model"`
	AwarenessDirs  []string `json:"awareness_dirs"`
	BindingSession string   `json:"binding_session"`
	// The letter store is append-only: a letter cannot be written twice, so a
	// turnover that failed after filing retries against the one on disk.
	LetterPath      string   `json:"letter_path"`
	LetterSession   string   `json:"letter_session"`
	AutonomousWakes []string `json:"autonomous_wakes"`
}

func (m Member) LaunchAgent() string {
	if agent := strings.TrimSpace(strings.ToLower(m.Agent)); agent != "" {
		return agent
	}
	return DefaultAgent
}

func (m Member) FiledLetterFor(sessionID string) (string, bool) {
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

func (m Member) Encode() ([]byte, error) {
	if m.AwarenessDirs == nil {
		m.AwarenessDirs = []string{}
	}
	if m.AutonomousWakes == nil {
		m.AutonomousWakes = []string{}
	}
	return json.Marshal(m)
}

// Unknown keys are ignored on purpose: a record written by a later attn stays
// readable by an older one.
func Decode(body []byte) (Member, error) {
	var member Member
	if err := json.Unmarshal(body, &member); err != nil {
		return Member{}, fmt.Errorf("this member's stored record is not readable: %w", err)
	}
	return member, nil
}

// The longest real id is 7 characters (`trellis`); 40 is a tripwire only a
// generated string touches.
const MaxIDChars = 40

var memberIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// The name the daemon moves a seed under when it acts on its own. No crew file
// can claim it, and it is displayed the way the product is written: lowercase.
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

// app/src/utils/crewName.ts keeps the same rule; the id itself stays lowercase
// in paths, arguments, JSON and the store.
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

func HolderName(member, session string) string {
	if strings.TrimSpace(member) != "" {
		return DisplayName(member)
	}
	return strings.TrimSpace(session)
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
			CharterPath:   charter,
			HomeDir:       home,
			AwarenessDirs: []string{},
		})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members, nil
}
