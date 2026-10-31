package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
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
	Key            who.MemberKey      `json:"-"`
	Name           string             `json:"-"`
	ProfileID      string             `json:"-"`
	CWD            string             `json:"cwd"`
	Agent          string             `json:"agent"`
	Model          string             `json:"model"`
	Effort         string             `json:"effort"`
	AwarenessDirs  []string           `json:"awareness_dirs"`
	BindingSession protocol.SessionID `json:"binding_session"`

	LetterFile      string             `json:"letter_file"`
	LetterSession   protocol.SessionID `json:"letter_session"`
	AutonomousWakes []string           `json:"autonomous_wakes"`
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

const MaxNameChars = 40

const KeyRefPrefix = "member:"

var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

var idShapedName = regexp.MustCompile(`^[A-Za-z]-[A-Za-z0-9]{6}$`)

func ValidateName(name string) error {
	if name == "" {
		return errors.New("a crew member name is required")
	}
	if len(name) > MaxNameChars {
		return fmt.Errorf("%q is too long: a crew member name's limit is %d characters, asked for %d", name, MaxNameChars, len(name))
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%q is not a crew member name: letters, digits and - only, starting with a letter", name)
	}
	switch strings.ToLower(name) {
	case "chief", DaemonID, "user", "you":
		return fmt.Errorf("%q is reserved; attn uses it to address someone else", name)
	}
	if idShapedName.MatchString(name) {
		return fmt.Errorf("%q looks like an ID; choose a name that does not", name)
	}
	return nil
}

func NameFromKey(key string) string {
	if name := DisplayName(key); ValidateName(name) == nil {
		return name
	}
	return key + "-crew"
}

func UnusedName(name string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	for number := 2; ; number++ {
		suffix := "-" + strconv.Itoa(number)
		stem := name
		if len(stem)+len(suffix) > MaxNameChars {
			stem = stem[:MaxNameChars-len(suffix)]
		}
		if candidate := stem + suffix; !taken(candidate) {
			return candidate
		}
	}
}

func Resolve(ref string, members []Member) (Member, bool) {
	ref = strings.TrimSpace(ref)
	key, byKey := strings.CutPrefix(ref, KeyRefPrefix)
	for _, m := range members {
		if (byKey && strings.EqualFold(m.ID, key)) || (!byKey && ref != "" && strings.EqualFold(m.Name, ref)) {
			return m, true
		}
	}
	return Member{}, false
}

func Keyed(key string, members []Member) (Member, bool) {
	for _, m := range members {
		if strings.EqualFold(m.ID, key) {
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
