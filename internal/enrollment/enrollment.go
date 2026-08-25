// Design: docs/plans/2026-08-10-home-garden-crew-arc.md
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
	PlanPath         = "docs/plans/2026-08-10-home-garden-crew-arc.md"
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
	return "outpost of " + s.HomeDaemonID
}

func (s Status) RequireHome(surface string) error {
	if s.IsHome() {
		return nil
	}
	return &FencedError{Surface: surface, DaemonID: s.DaemonID, HomeDaemonID: s.HomeDaemonID}
}

type FencedError struct {
	Surface      string
	DaemonID     string
	HomeDaemonID string
}

func (e *FencedError) Error() string {
	surface := strings.TrimSpace(e.Surface)
	if surface == "" {
		surface = "this state"
	}
	home := strings.TrimSpace(e.HomeDaemonID)
	if home == "" {
		return fmt.Sprintf(
			"refused %s on this daemon: its enrollment record is unreadable, so attn cannot tell whether this is a home.\n"+
				"  this daemon: %s\n"+
				"Run `attn enrollment` here to see the record, then `attn enrollment leave` to declare this daemon its own home.\n"+
				"Why: %s",
			surface, displayID(e.DaemonID), PlanPath,
		)
	}
	return fmt.Sprintf(
		"refused %s on this daemon: it is an outpost, and home-level state lives at its home.\n"+
			"  this daemon: %s (outpost)\n"+
			"  its home:    %s\n"+
			"The garden and the crew have exactly one owner — the home daemon — and the uplink that would\n"+
			"carry this ask home is not built yet.\n"+
			"Do this on the home daemon (%s), or make this daemon its own home again with `attn enrollment leave`.\n"+
			"Why: %s",
		surface, displayID(e.DaemonID), home, home, PlanPath,
	)
}

func displayID(id string) string {
	if strings.TrimSpace(id) == "" {
		return "unknown"
	}
	return id
}

type ForeignHomeError struct {
	DaemonID     string
	CurrentHome  string
	RequestedBy  string
	DataRootHint string
}

func (e *ForeignHomeError) Error() string {
	return fmt.Sprintf(
		"this daemon (%s) is already an outpost of %s; %s asked to take it over.\n"+
			"Enrollment is never overwritten silently: a daemon has exactly one home, and moving it moves its\n"+
			"garden and crew asks with it.\n"+
			"To move it, run `attn enrollment leave` here — that makes it a home again — then sync it from %s.\n"+
			"Why: %s",
		displayID(e.DaemonID), e.CurrentHome, e.RequestedBy, e.RequestedBy, PlanPath,
	)
}

type Result struct {
	Status       string `json:"status"`
	DaemonID     string `json:"daemon_id"`
	HomeDaemonID string `json:"home_daemon_id"`
	PreviousHome string `json:"previous_home_daemon_id,omitempty"`
	Message      string `json:"message"`
}

func (r Result) Changed() bool {
	return r.Status == "enrolled" || r.Status == "left"
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

func Enroll(dataRoot, homeDaemonID string) (Result, error) {
	if !ValidDaemonID(homeDaemonID) {
		return Result{}, fmt.Errorf("invalid home daemon id %q (want d- followed by 32 hex characters)", homeDaemonID)
	}
	daemonID, err := ReadDaemonID(dataRoot)
	if err != nil && !errors.Is(err, ErrNoDaemonID) {
		return Result{}, err
	}
	if daemonID == homeDaemonID {
		return Result{}, fmt.Errorf("a daemon cannot enroll to itself (%s)", homeDaemonID)
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
	current := ""
	if err == nil {
		current = record.HomeDaemonID
	}

	switch current {
	case homeDaemonID:
		return Result{
			Status:       "unchanged",
			DaemonID:     daemonID,
			HomeDaemonID: homeDaemonID,
			Message:      fmt.Sprintf("already an outpost of %s", homeDaemonID),
		}, nil
	case "", daemonID:
	default:
		refusal := &ForeignHomeError{DaemonID: daemonID, CurrentHome: current, RequestedBy: homeDaemonID}
		return Result{
			Status:       "refused",
			DaemonID:     daemonID,
			HomeDaemonID: current,
			PreviousHome: current,
			Message:      refusal.Error(),
		}, refusal
	}

	if err := writeRecord(recordPath, Record{HomeDaemonID: homeDaemonID, RecordedAt: nowStamp()}); err != nil {
		return Result{}, err
	}
	return Result{
		Status:       "enrolled",
		DaemonID:     daemonID,
		HomeDaemonID: homeDaemonID,
		PreviousHome: current,
		Message:      fmt.Sprintf("enrolled as an outpost of %s", homeDaemonID),
	}, nil
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
			Message:      "already a home daemon",
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
		Message:      "now a home daemon; it owns its own garden and crew",
	}, nil
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

// lockPath takes an exclusive flock on <path>.lock and returns the release. The lock
// file is never removed: unlinking it lets a later locker hold an uncontended lock.
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
