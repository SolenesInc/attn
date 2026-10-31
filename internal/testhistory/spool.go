package testhistory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Spool struct {
	Dir string
}

type invocation struct {
	Receipt  Receipt `json:"receipt"`
	GoModule string  `json:"go_module,omitempty"`
}

type Working struct {
	spool       Spool
	dir         string
	lock        *os.File
	ResultsPath string
}

func (s Spool) ReceiptsDir() string { return filepath.Join(s.Dir, "receipts") }
func (s Spool) workingRoot() string { return filepath.Join(s.Dir, "working") }

func (s Spool) ReceiptPath(id string) string {
	return filepath.Join(s.ReceiptsDir(), id+".json")
}

func resultsFile(runner Runner) string {
	if runner == RunnerGo || runner == RunnerVitest {
		return string(runner) + ".jsonl"
	}
	return string(runner) + ".json"
}

func (s Spool) Begin(receipt Receipt, goModule string) (*Working, error) {
	if err := os.MkdirAll(s.workingRoot(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.ReceiptsDir(), 0o755); err != nil {
		return nil, err
	}
	staging := filepath.Join(s.workingRoot(), "."+receipt.InvocationID)
	if err := os.Mkdir(staging, 0o755); err != nil {
		return nil, err
	}
	lock, err := lockFile(filepath.Join(staging, "lock"), true)
	if err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	data, err := json.Marshal(invocation{Receipt: receipt, GoModule: goModule})
	if err == nil {
		err = os.WriteFile(filepath.Join(staging, "invocation.json"), data, 0o644)
	}
	dir := filepath.Join(s.workingRoot(), receipt.InvocationID)
	if err == nil {
		err = os.Rename(staging, dir)
	}
	if err != nil {
		_ = lock.Close()
		_ = os.RemoveAll(staging)
		return nil, err
	}
	return &Working{spool: s, dir: dir, lock: lock, ResultsPath: filepath.Join(dir, resultsFile(receipt.Runner))}, nil
}

func (w *Working) Lock() *os.File { return w.lock }

type Ending struct {
	Exit        *Exit
	FinishedAt  time.Time
	Interrupted string
}

func (w *Working) Seal(ending Ending) (Receipt, error) {
	defer func() { _ = w.lock.Close() }()
	return w.spool.seal(w.dir, &ending)
}

func (s Spool) seal(dir string, ending *Ending) (Receipt, error) {
	data, err := os.ReadFile(filepath.Join(dir, "invocation.json"))
	if err != nil {
		return Receipt{}, err
	}
	var started invocation
	if err := json.Unmarshal(data, &started); err != nil {
		return Receipt{}, fmt.Errorf("%s: %w", filepath.Join(dir, "invocation.json"), err)
	}
	receipt := started.Receipt
	raw, err := os.ReadFile(filepath.Join(dir, resultsFile(receipt.Runner)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Receipt{}, err
	}
	observed := Observe(receipt.Runner, raw, started.GoModule)
	receipt.Results = observed.Results
	if receipt.Results == nil {
		receipt.Results = []Result{}
	}
	if ending != nil {
		finished := ending.FinishedAt.UTC()
		wall := finished.Sub(receipt.Command.StartedAt).Milliseconds()
		receipt.Command.FinishedAt, receipt.Command.WallMS = &finished, &wall
		receipt.Command.Exit, receipt.Command.Interrupted = ending.Exit, ending.Interrupted
	}
	receipt.Complete = ending != nil && ending.Exit != nil && observed.Finished
	if err := s.store(receipt); err != nil {
		return receipt, err
	}
	return receipt, os.RemoveAll(dir)
}

func Observe(runner Runner, raw []byte, goModule string) Observed {
	switch runner {
	case RunnerGo:
		return ObserveGo(raw, goModule)
	case RunnerVitest:
		return ObserveVitest(raw)
	case RunnerPlaywright:
		return ObservePlaywright(raw)
	default:
		return ObserveNative(raw)
	}
}

func (s Spool) store(receipt Receipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	final := s.ReceiptPath(receipt.InvocationID)
	temp, err := os.CreateTemp(s.ReceiptsDir(), ".seal-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(temp.Name(), final); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		existing, readErr := os.ReadFile(final)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("receipt %s is already sealed with different content; keeping the sealed one", final)
		}
	}
	return nil
}

type Recovery struct {
	InvocationID string
	Outcome      string
	Err          error
}

func (s Spool) Recover() ([]Recovery, error) {
	entries, err := os.ReadDir(s.workingRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recoveries []Recovery
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(id, ".") {
			continue
		}
		recoveries = append(recoveries, s.recoverOne(id))
	}
	return recoveries, nil
}

func (s Spool) recoverOne(id string) Recovery {
	dir := filepath.Join(s.workingRoot(), id)
	lock, err := lockFile(filepath.Join(dir, "lock"), false)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return Recovery{InvocationID: id, Outcome: "running"}
	}
	if err != nil {
		return Recovery{InvocationID: id, Err: err}
	}
	defer func() { _ = lock.Close() }()
	if _, err := os.Stat(s.ReceiptPath(id)); err == nil {
		return Recovery{InvocationID: id, Outcome: "already sealed", Err: os.RemoveAll(dir)}
	}
	if _, err := s.seal(dir, nil); err != nil {
		return Recovery{InvocationID: id, Err: err}
	}
	return Recovery{InvocationID: id, Outcome: "sealed incomplete"}
}

func lockFile(path string, create bool) (*os.File, error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
