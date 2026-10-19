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

type Record struct {
	HomeDaemonID string `json:"home_daemon_id"`
	RecordedAt   string `json:"recorded_at,omitempty"`
}

type Status struct {
	DaemonID     string `json:"daemon_id"`
	HomeDaemonID string `json:"home_daemon_id"`
}

func (s Status) IsHome() bool {
	return s.DaemonID != "" && s.DaemonID == s.HomeDaemonID
}

func (s Status) Describe() string {
	if s.IsHome() {
		return "home"
	}
	if strings.TrimSpace(s.HomeDaemonID) == "" {
		return "unknown"
	}
	return "enrolled to " + s.HomeDaemonID
}

type Result struct {
	Status       string `json:"status"`
	DaemonID     string `json:"daemon_id"`
	HomeDaemonID string `json:"home_daemon_id"`
	PreviousHome string `json:"previous_home_daemon_id,omitempty"`
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

	id, err := newDaemonID()
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

	record, err := readRecord(recordPath)
	switch {
	case err == nil:
		return Status{DaemonID: daemonID, HomeDaemonID: record.HomeDaemonID}, nil
	case errors.Is(err, ErrNoRecord):
		if err := writeRecord(recordPath, Record{HomeDaemonID: daemonID, RecordedAt: nowStamp()}); err != nil {
			return Status{}, err
		}
		return Status{DaemonID: daemonID, HomeDaemonID: daemonID}, nil
	default:
		return Status{}, err
	}
}

func Load(dataRoot string) (Status, error) {
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil && !errors.Is(err, ErrNoDaemonID) {
		return Status{}, err
	}
	record, err := readRecord(filepath.Join(dataRoot, RecordFileName))
	if err != nil {
		return Status{DaemonID: daemonID}, err
	}
	return Status{DaemonID: daemonID, HomeDaemonID: record.HomeDaemonID}, nil
}

func Leave(dataRoot string) (Result, error) {
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil {
		return Result{}, err
	}

	recordPath := filepath.Join(dataRoot, RecordFileName)
	unlock, err := lockPath(recordPath)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	record, err := readRecord(recordPath)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return Result{}, err
	}
	previous := ""
	if err == nil {
		previous = record.HomeDaemonID
	}
	if previous == daemonID {
		return Result{
			Status:       "unchanged",
			DaemonID:     daemonID,
			HomeDaemonID: daemonID,
			Message:      "not enrolled to another daemon",
		}, nil
	}
	if err := writeRecord(recordPath, Record{HomeDaemonID: daemonID, RecordedAt: nowStamp()}); err != nil {
		return Result{}, err
	}
	return Result{
		Status:       "left",
		DaemonID:     daemonID,
		HomeDaemonID: daemonID,
		PreviousHome: previous,
		Message:      "no longer enrolled to another daemon",
	}, nil
}

func RecordHome(dataRoot, homeID string) (Status, error) {
	if !ValidDaemonID(homeID) {
		return Status{}, fmt.Errorf("invalid home daemon id %q", homeID)
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
	record, err := readRecord(recordPath)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return Status{}, err
	}
	if err == nil && record.HomeDaemonID != daemonID && record.HomeDaemonID != homeID {
		return Status{}, fmt.Errorf("outpost %s is paired to home %s; home %s refused; run attn enrollment leave on the outpost to release it", daemonID, record.HomeDaemonID, homeID)
	}
	if err != nil || record.HomeDaemonID != homeID {
		if err := writeRecord(recordPath, Record{HomeDaemonID: homeID, RecordedAt: nowStamp()}); err != nil {
			return Status{}, err
		}
	}
	return Status{DaemonID: daemonID, HomeDaemonID: homeID}, nil
}

func ValidDaemonID(id string) bool {
	if !strings.HasPrefix(id, "d-") {
		return false
	}
	if len(id) != 34 {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}

func newDaemonID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate daemon id: %w", err)
	}
	return "d-" + hex.EncodeToString(buf), nil
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

func readRecord(path string) (Record, error) {
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
	if !ValidDaemonID(record.HomeDaemonID) {
		return Record{}, fmt.Errorf("enrollment record %s names an invalid home daemon id %q", path, record.HomeDaemonID)
	}
	return record, nil
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
