package enrollment

import (
	"cmp"
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

// Record is enrollment.json. A record written before homes had ids holds only HomeDaemonID. The builds before
// homes had ids read only HomeDaemonID, and key the state they store by it: at the home it is the home's own
// daemon id, on an outpost the daemon id of the home it bridged with last.
type Record struct {
	Role          Role   `json:"role,omitempty"`
	HomeID        string `json:"home_id,omitempty"`
	HomeName      string `json:"home_name,omitempty"`
	HomeDaemonID  string `json:"home_daemon_id"`
	MachineID     string `json:"machine_id,omitempty"`
	HomeMachineID string `json:"home_machine_id,omitempty"`
	Dial          string `json:"dial,omitempty"`
	RecordedAt    string `json:"recorded_at,omitempty"`
}

// Status is an enrollment as this build reads it. It names the home by HomeDaemonID only while HomeID is empty.
type Status struct {
	DaemonID      string `json:"daemon_id"`
	Role          Role   `json:"role"`
	HomeID        string `json:"home_id,omitempty"`
	HomeName      string `json:"home_name,omitempty"`
	HomeDaemonID  string `json:"home_daemon_id,omitempty"`
	MachineID     string `json:"machine_id,omitempty"`
	HomeMachineID string `json:"home_machine_id,omitempty"`
	Dial          string `json:"dial,omitempty"`
}

// UnmarshalJSON reads a status printed by any build, including one from before homes had ids.
func (s *Status) UnmarshalJSON(data []byte) error {
	type plain Status
	if err := json.Unmarshal(data, (*plain)(s)); err != nil {
		return err
	}
	switch {
	case s.Role == "" && s.HomeDaemonID != "":
		s.Role = RoleOutpost
		if s.HomeDaemonID == s.DaemonID {
			s.Role, s.HomeDaemonID = RoleHome, ""
		}
	case s.HomeID != "":
		s.HomeDaemonID = ""
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

// Adopting says a build before homes had ids may have stored state under HomeDaemonID, to key on HomeID.
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

// Home is a home as one of its daemons presents it: its own machine id, and the id it knows this machine by.
type Home struct {
	ID, DaemonID, Name       string
	MachineID, PeerMachineID string
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
	switch {
	case err == nil && record.Role == RoleOutpost:
		return record.status(daemonID), nil
	case err == nil && record.HomeID != "":
		if record.HomeDaemonID == daemonID {
			return record.status(daemonID), nil
		}
		record.HomeDaemonID = daemonID
	default:
		homeID, err := newID("h-")
		if err != nil {
			return Status{}, err
		}
		record = Record{Role: RoleHome, HomeID: homeID, HomeDaemonID: daemonID}
	}
	record.RecordedAt = nowStamp()
	if err := writeRecord(recordPath, record); err != nil {
		return Status{}, err
	}
	return record.status(daemonID), nil
}

func (r Record) status(daemonID string) Status {
	return Status{DaemonID: daemonID, Role: r.Role, HomeID: r.HomeID, HomeName: r.HomeName, HomeDaemonID: r.HomeDaemonID, MachineID: r.MachineID, HomeMachineID: r.HomeMachineID, Dial: r.Dial}
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

// RecordHome enrolls this data folder to home, whose daemon is bridging now, and dial is the ssh destination this
// machine dials it by, empty when the home dials. A record from before homes had ids
// takes the home id when home's daemon is the one it names.
func RecordHome(dataRoot string, home Home, dial string) (Status, error) {
	if !ValidHomeID(home.ID) {
		return Status{}, fmt.Errorf("home %s sent invalid home id %q", home.DaemonID, home.ID)
	}
	if !ValidDaemonID(home.DaemonID) {
		return Status{}, fmt.Errorf("home %s sent invalid daemon id %q", home.ID, home.DaemonID)
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
	if err == nil {
		if err := record.status(daemonID).CheckHome(home); err != nil {
			return Status{}, err
		}
	}
	next := Record{Role: RoleOutpost, HomeID: home.ID, HomeName: home.Name, HomeDaemonID: home.DaemonID, MachineID: cmp.Or(home.PeerMachineID, record.MachineID), HomeMachineID: cmp.Or(home.MachineID, record.HomeMachineID), Dial: dial}
	if err != nil || record.Role != next.Role || record.HomeID != next.HomeID || record.HomeName != next.HomeName || record.HomeDaemonID != next.HomeDaemonID || record.MachineID != next.MachineID || record.HomeMachineID != next.HomeMachineID || record.Dial != next.Dial {
		next.RecordedAt = nowStamp()
		if err := writeRecord(recordPath, next); err != nil {
			return Status{}, err
		}
	}
	return next.status(daemonID), nil
}

// Leave makes this data folder its own home again, under a new home id, when it is enrolled to homeID. It writes
// what attn enrollment leave writes.
func Leave(dataRoot, homeID string) (Status, error) {
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
	if record.Role != RoleOutpost || record.HomeID != homeID {
		return record.status(daemonID), nil
	}
	ownHome, err := newID("h-")
	if err != nil {
		return Status{}, err
	}
	record = Record{Role: RoleHome, HomeID: ownHome, HomeDaemonID: daemonID, RecordedAt: nowStamp()}
	if err := writeRecord(recordPath, record); err != nil {
		return Status{}, err
	}
	return record.status(daemonID), nil
}

// RecordMachineID records the id this home's database gives its own machine.
func RecordMachineID(dataRoot, machineID string) (Status, error) {
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
	if record.Role == RoleHome && (record.MachineID != machineID || record.HomeDaemonID != daemonID) {
		record.MachineID, record.HomeDaemonID, record.RecordedAt = machineID, daemonID, nowStamp()
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
