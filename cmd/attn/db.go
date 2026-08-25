package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/store"
)

func runDB() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeDBHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "restore":
		if hasHelpFlag(os.Args[3:]) {
			writeDBHelp(os.Stdout)
			return
		}
		runDBRestore(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "db: unknown command %q\n", os.Args[2])
		writeDBHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeDBHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn db <command>

commands:
  restore [path|latest]
        restore attn.db from a rotating backup (default: latest). The daemon
        must be stopped first. The current attn.db is preserved (renamed, never
        deleted) as attn.db.pre-restore-<UTC timestamp> before the backup is
        copied into place.

        path defaults to "latest": the newest rotating attn-<timestamp>.db
        snapshot in the profile's backups directory. Pass an explicit path to
        restore from any snapshot, including a pre-migration one
        (attn-premigration-<version>-<timestamp>.db).
`)
}

func runDBRestore(args []string) {
	source := "latest"
	if len(args) > 0 {
		source = args[0]
	}

	dbPath := config.DBPath()
	backupsDir := filepath.Join(config.DataDir(), "backups")
	pidPath := filepath.Join(config.DataDir(), "attn.pid")

	restoredFrom, preservedAs, err := restoreDatabase(dbPath, backupsDir, source, pidPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn db restore: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("restored attn.db from: %s\n", restoredFrom)
	if preservedAs != "" {
		fmt.Printf("previous attn.db preserved as: %s\n", preservedAs)
	} else {
		fmt.Println("no previous attn.db existed to preserve")
	}
	fmt.Println("start the daemon when ready (e.g. `attn daemon ensure`, or reopen the app)")
}

func restoreDatabase(dbPath, backupsDir, source, pidPath string) (restoredFrom, preservedAs string, err error) {
	release, err := acquireDaemonLock(pidPath)
	if err != nil {
		return "", "", err
	}
	defer release()

	srcPath := strings.TrimSpace(source)
	if srcPath == "" || srcPath == "latest" {
		srcPath, err = latestRotatingBackup(backupsDir)
		if err != nil {
			return "", "", err
		}
	}

	srcInfo, statErr := os.Stat(srcPath)
	if statErr != nil {
		return "", "", fmt.Errorf("backup file %s: %w", srcPath, statErr)
	}
	if !srcInfo.Mode().IsRegular() {
		return "", "", fmt.Errorf("backup source %s is not a regular file", srcPath)
	}

	dstExists := false
	if dstInfo, statErr := os.Stat(dbPath); statErr == nil {
		dstExists = true
		if os.SameFile(srcInfo, dstInfo) {
			return "", "", fmt.Errorf("backup source %s is the live database at %s; choose a snapshot from the backups directory instead", srcPath, dbPath)
		}
	} else if !os.IsNotExist(statErr) {
		return "", "", fmt.Errorf("stat existing db %s: %w", dbPath, statErr)
	}

	// Stage fully before touching the live path: a bad source is a no-op failure.
	stagedPath, err := stageBackupCopy(srcPath, dbPath)
	if err != nil {
		return "", "", fmt.Errorf("stage backup %s: %w", srcPath, err)
	}
	stagedNeedsCleanup := true
	defer func() {
		if stagedNeedsCleanup {
			_ = os.Remove(stagedPath)
		}
	}()

	if dstExists {
		preservedAs, err = preserveExistingDB(dbPath)
		if err != nil {
			return "", "", err
		}
	} else {
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	}

	if err := os.Rename(stagedPath, dbPath); err != nil {
		if preservedAs != "" {
			_ = os.Rename(preservedAs, dbPath)
			for _, suffix := range []string{"-wal", "-shm"} {
				_ = os.Rename(preservedAs+suffix, dbPath+suffix)
			}
		}
		return "", "", fmt.Errorf("move staged backup into place: %w", err)
	}
	stagedNeedsCleanup = false

	return srcPath, preservedAs, nil
}

// Never deleted: sidecars can hold uncheckpointed data.
func preserveExistingDB(dbPath string) (string, error) {
	return preserveExistingDBAt(dbPath, time.Now, os.Rename)
}

func preserveExistingDBAt(dbPath string, now func() time.Time, rename func(oldpath, newpath string) error) (preservedAs string, err error) {
	base := dbPath + ".pre-restore-" + now().UTC().Format("20060102-150405")
	preservedAs = base
	for n := 2; ; n++ {
		free, err := preserveTargetFree(preservedAs)
		if err != nil {
			return "", fmt.Errorf("check preserve target %s: %w", preservedAs, err)
		}
		if free {
			break
		}
		preservedAs = fmt.Sprintf("%s-%d", base, n)
	}

	type move struct{ from, to string }
	var moved []move
	rollback := func() error {
		var rbErr error
		for i := len(moved) - 1; i >= 0; i-- {
			if err := os.Rename(moved[i].to, moved[i].from); err != nil {
				rbErr = errors.Join(rbErr, fmt.Errorf("restore %s: %w", moved[i].from, err))
			}
		}
		return rbErr
	}

	if err := rename(dbPath, preservedAs); err != nil {
		return "", fmt.Errorf("preserve existing db %s: %w", dbPath, err)
	}
	moved = append(moved, move{from: dbPath, to: preservedAs})

	for _, suffix := range []string{"-wal", "-shm"} {
		from, to := dbPath+suffix, preservedAs+suffix
		if err := rename(from, to); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if rbErr := rollback(); rbErr != nil {
				return "", fmt.Errorf("preserve existing db sidecar %s: %w (rollback also failed: %v)", from, err, rbErr)
			}
			return "", fmt.Errorf("preserve existing db sidecar %s: %w", from, err)
		}
		moved = append(moved, move{from: from, to: to})
	}
	return preservedAs, nil
}

func preserveTargetFree(candidate string) (bool, error) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(candidate + suffix); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, fmt.Errorf("stat %s: %w", candidate+suffix, err)
		}
	}
	return true, nil
}

// "latest" only ever means the newest routine rotation, never a stray or pre-migration snapshot.
func latestRotatingBackup(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read backups dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !store.IsRotatingBackupName(name) {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no rotating backups found in %s", dir)
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

// Stage into dstPath's own directory, which makes the final move a same-filesystem atomic rename.
func stageBackupCopy(src, dstPath string) (stagedPath string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(filepath.Dir(dstPath), filepath.Base(dstPath)+".restore-tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	succeeded = true
	return tmpPath, nil
}

// HOLDS the flock until release: probe-then-release would leave a window for a daemon or a second restore.
// flockFn is indirected for tests; only EWOULDBLOCK means a live daemon holds it.
var flockFn = syscall.Flock

func acquireDaemonLock(pidPath string) (release func(), err error) {
	lockFile, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pid file %s: %w", pidPath, err)
	}
	if flockErr := flockFn(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr != nil {
		lockFile.Close()
		if errors.Is(flockErr, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("the attn daemon is running; stop it first (quit the app, or `attn daemon stop`) before restoring the database")
		}
		// Indeterminate flock result: fail closed, never restore over a live daemon.
		return nil, fmt.Errorf("cannot determine daemon state: %w", flockErr)
	}
	// Stamp the sentinel over any stale pid so a concurrent `attn daemon stop`, which trusts only holder-written content, never signals it.
	// which trusts only holder-written content, never signals it.
	if err := lockFile.Truncate(0); err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("stamp non-daemon holder sentinel: %w", err)
	}
	if _, err := lockFile.WriteAt([]byte(daemonctl.NonDaemonHolderSentinel), 0); err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("stamp non-daemon holder sentinel: %w", err)
	}
	if err := lockFile.Sync(); err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("stamp non-daemon holder sentinel: %w", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			_ = lockFile.Close()
		})
	}
	return release, nil
}
