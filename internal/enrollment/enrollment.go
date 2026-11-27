package enrollment

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	DaemonIDFileName = "daemon-id"
	RecordFileName   = "enrollment.json"
)

var ErrNoRecord = errors.New("no enrollment record")

var ErrNoDaemonID = errors.New("no daemon id")

type Role string

const (
	RoleHome    Role = "home"
	RoleOutpost Role = "outpost"
)

// Record is enrollment.json. A record written before homes had ids holds only HomeDaemonID. A record keeps
// HomeDaemonID beside HomeID until the state stored under that daemon id carries the home id.
type Record struct {
	Role         Role   `json:"role,omitempty"`
	HomeID       string `json:"home_id,omitempty"`
	HomeName     string `json:"home_name,omitempty"`
	HomeDaemonID string `json:"home_daemon_id,omitempty"`
	RecordedAt   string `json:"recorded_at,omitempty"`
}

type Status struct {
	DaemonID     string `json:"daemon_id"`
	Role         Role   `json:"role"`
	HomeID       string `json:"home_id,omitempty"`
	HomeName     string `json:"home_name,omitempty"`
	HomeDaemonID string `json:"home_daemon_id,omitempty"`
}

// UnmarshalJSON reads a status printed by any build, including one from before homes had ids.
func (s *Status) UnmarshalJSON(data []byte) error {
	type plain Status
	if err := json.Unmarshal(data, (*plain)(s)); err != nil {
		return err
	}
	if s.Role == "" && s.HomeDaemonID != "" {
		s.Role = RoleOutpost
		if s.HomeDaemonID == s.DaemonID {
			s.Role, s.HomeDaemonID = RoleHome, ""
		}
	}
	return nil
}

func (s Status) IsHome() bool { return s.Role == RoleHome }

func (s Status) IsOutpost() bool { return s.Role == RoleOutpost }

// CopiedFrom is the key of the rows this outpost holds as copies of its home's.
func (s Status) CopiedFrom() string {
	switch {
	case !s.IsOutpost():
		return ""
	case s.HomeID != "":
		return s.HomeID
	default:
		return s.HomeDaemonID
	}
}

// AwaitsHomeID says the record names its home only by the home's daemon id, from before homes had ids.
func (s Status) AwaitsHomeID() bool { return s.IsOutpost() && s.HomeID == "" }

// Adopting says state may still be stored under the daemon id the home id replaced.
func (s Status) Adopting() bool { return s.HomeID != "" && s.HomeDaemonID != "" }

func (s Status) homeLabel() string {
	id := s.HomeID
	if id == "" {
		id = s.HomeDaemonID
	}
	if s.HomeName == "" {
		return id
	}
	return s.HomeName + " (" + id + ")"
}

// Home is a home as one of its daemons presents it.
type Home struct {
	ID, DaemonID, Name string
}

func (h Home) label() string {
	if h.Name == "" {
		return h.ID
	}
	return h.Name + " (" + h.ID + ")"
}

// CheckHome refuses home when this daemon is enrolled to another home.
func (s Status) CheckHome(home Home) error {
	switch {
	case !s.IsOutpost(), s.HomeID != "" && s.HomeID == home.ID, s.AwaitsHomeID() && s.HomeDaemonID == home.DaemonID:
		return nil
	}
	return fmt.Errorf("outpost %s is paired to home %s; home %s refused; run attn enrollment leave on the outpost to release it", s.DaemonID, s.homeLabel(), home.label())
}

func (s Status) Describe() string {
	switch s.Role {
	case RoleHome:
		return "home"
	case RoleOutpost:
		return "enrolled to " + s.homeLabel()
	default:
		return "unknown"
	}
}

type Result struct {
	Status       string `json:"status"`
	DaemonID     string `json:"daemon_id"`
	HomeID       string `json:"home_id"`
	PreviousHome string `json:"previous_home_id,omitempty"`
	Message      string `json:"message"`
}

func EnsureDaemonID(dataRoot string) (string, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return "", fmt.Errorf("missing data root")
	}
	if err := os.MkdirAll(dataRoot, 0700); err != nil {
		return "", fmt.Errorf("create data root: %w", err)
	}

	idPath := filepath.Join(dataRoot, DaemonIDFileName)
	unlock, err := lockPath(idPath)
	if err != nil {
		return "", err
	}
	defer unlock()

	if id, err := readDaemonIDFile(idPath); err == nil && id != "" {
		return id, nil
	}

	id, err := newID("d-")
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(idPath, []byte(id+"\n")); err != nil {
		return "", fmt.Errorf("persist daemon id: %w", err)
	}
	return id, nil
}

func ReadDaemonID(dataRoot string) (string, error) {
	id, err := readDaemonIDFile(filepath.Join(dataRoot, DaemonIDFileName))
	if err != nil {
		return "", err
	}
	return id, nil
}

func Ensure(dataRoot, daemonID string) (Status, error) {
	if !ValidDaemonID(daemonID) {
		return Status{}, fmt.Errorf("invalid daemon id %q", daemonID)
	}
	recordPath := filepath.Join(dataRoot, RecordFileName)
	unlock, err := lockPath(recordPath)
	if err != nil {
		return Status{}, err
	}
	defer unlock()

	record, err := readRecord(recordPath, daemonID)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return Status{}, err
	}
	if err == nil && (record.Role != RoleHome || record.HomeID != "") {
		return record.status(daemonID), nil
	}
	var replaced string
	if err == nil {
		replaced = daemonID
	}
	homeID, err := newID("h-")
	if err != nil {
		return Status{}, err
	}
	record = Record{Role: RoleHome, HomeID: homeID, HomeDaemonID: replaced, RecordedAt: nowStamp()}
	if err := writeRecord(recordPath, record); err != nil {
		return Status{}, err
	}
	return record.status(daemonID), nil
}

func (r Record) status(daemonID string) Status {
	return Status{DaemonID: daemonID, Role: r.Role, HomeID: r.HomeID, HomeName: r.HomeName, HomeDaemonID: r.HomeDaemonID}
}

func Load(dataRoot string) (Status, error) {
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil && !errors.Is(err, ErrNoDaemonID) {
		return Status{}, err
	}
	record, err := readRecord(filepath.Join(dataRoot, RecordFileName), daemonID)
	if err != nil {
		return Status{DaemonID: daemonID}, err
	}
	return record.status(daemonID), nil
}

// RecordHome enrolls this data folder to home, whose daemon is bridging now. A record from before homes had ids
// takes the home id when home's daemon is the one it names; the result then still carries that daemon id, until
// FinishAdoption.
func RecordHome(dataRoot string, home Home) (Status, error) {
	if !ValidHomeID(home.ID) {
		return Status{}, fmt.Errorf("home %s sent invalid home id %q", home.DaemonID, home.ID)
	}
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil {
		return Status{}, err
	}
	recordPath := filepath.Join(dataRoot, RecordFileName)
	unlock, err := lockPath(recordPath)
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	record, err := readRecord(recordPath, daemonID)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return Status{}, err
	}
	var replaced string
	if err == nil {
		current := record.status(daemonID)
		if err := current.CheckHome(home); err != nil {
			return Status{}, err
		}
		if current.AwaitsHomeID() || current.Adopting() {
			replaced = current.HomeDaemonID
		}
	}
	next := Record{Role: RoleOutpost, HomeID: home.ID, HomeName: home.Name, HomeDaemonID: replaced}
	if err != nil || record.Role != next.Role || record.HomeID != next.HomeID || record.HomeName != next.HomeName || record.HomeDaemonID != next.HomeDaemonID {
		next.RecordedAt = nowStamp()
		if err := writeRecord(recordPath, next); err != nil {
			return Status{}, err
		}
	}
	return next.status(daemonID), nil
}

// FinishAdoption forgets the daemon id the home id replaced, once no copy is stored under it.
func FinishAdoption(dataRoot string) (Status, error) {
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil {
		return Status{}, err
	}
	recordPath := filepath.Join(dataRoot, RecordFileName)
	unlock, err := lockPath(recordPath)
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	record, err := readRecord(recordPath, daemonID)
	if err != nil {
		return Status{}, err
	}
	if record.status(daemonID).Adopting() {
		record.HomeDaemonID, record.RecordedAt = "", nowStamp()
		if err := writeRecord(recordPath, record); err != nil {
			return Status{}, err
		}
	}
	return record.status(daemonID), nil
}

func ValidDaemonID(id string) bool { return validID(id, "d-") }

func ValidHomeID(id string) bool { return validID(id, "h-") }

func validID(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) || len(id) != 34 {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}

func newID(prefix string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate %sid: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(buf), nil
}

func readDaemonIDFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoDaemonID
		}
		return "", fmt.Errorf("read daemon id: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if !ValidDaemonID(id) {
		return "", ErrNoDaemonID
	}
	return id, nil
}

func readRecord(path, daemonID string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, ErrNoRecord
		}
		return Record{}, fmt.Errorf("read enrollment record: %w", err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("parse enrollment record %s: %w", path, err)
	}
	if record.HomeDaemonID != "" && !ValidDaemonID(record.HomeDaemonID) {
		return Record{}, fmt.Errorf("enrollment record %s names an invalid home daemon id %q", path, record.HomeDaemonID)
	}
	switch record.Role {
	case "":
		if record.HomeDaemonID == "" || record.HomeID != "" {
			return Record{}, fmt.Errorf("enrollment record %s has no role", path)
		}
		record.Role = RoleOutpost
		if record.HomeDaemonID == daemonID {
			record.Role, record.HomeDaemonID, record.HomeName = RoleHome, "", ""
		}
		return record, nil
	case RoleHome:
		if record.HomeID != "" && !ValidHomeID(record.HomeID) {
			return Record{}, fmt.Errorf("enrollment record %s names an invalid home id %q", path, record.HomeID)
		}
		return record, nil
	case RoleOutpost:
		if !ValidHomeID(record.HomeID) {
			return Record{}, fmt.Errorf("enrollment record %s names an invalid home id %q", path, record.HomeID)
		}
		return record, nil
	default:
		return Record{}, fmt.Errorf("enrollment record %s has unknown role %q", path, record.Role)
	}
}

func writeRecord(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode enrollment record: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create data root: %w", err)
	}
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("persist enrollment record: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func lockPath(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create data root: %w", err)
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open %s lock: %w", filepath.Base(path), err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("lock %s: %w", filepath.Base(path), err)
	}
	return func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
	}, nil
}

func nowStamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
