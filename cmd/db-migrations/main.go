package main

import (
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const usage = `Usage:
  go run ./cmd/db-migrations new [--go] <slug>
  go run ./cmd/db-migrations check-history [--base <git-ref>]

Use lowercase words separated by underscores for the slug.
Edit the created migration, then run make generate-schema.
`

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)
var goMigrationPattern = regexp.MustCompile(`^migration_(?:[0-9]{14}|[0-9]{16})_[a-z0-9]+(?:_[a-z0-9]+)*\.go$`)

func main() {
	cwd, err := os.Getwd()
	if err == nil {
		err = run(os.Args[1:], cwd, time.Now(), os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "db-migrations:", err)
		os.Exit(1)
	}
}

func run(args []string, cwd string, now time.Time, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("expected new or check-history; use db-migrations --help")
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	switch args[0] {
	case "new":
		goMigration := flags.Bool("go", false, "scaffold a registered Go data migration")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("new requires one slug; use new [--go] <slug>")
		}
		root, err := repositoryRoot(cwd)
		if err != nil {
			return err
		}
		file, err := newMigration(root, flags.Arg(0), *goMigration, now)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, file)
		return err
	case "check-history":
		base := flags.String("base", "origin/next", "Git revision containing merged migrations")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("check-history accepts only --base <git-ref>")
		}
		root, err := repositoryRoot(cwd)
		if err != nil {
			return err
		}
		return checkHistory(root, *base, stdout)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func newMigration(root, slug string, goMigration bool, now time.Time) (string, error) {
	if !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("invalid slug %q; use lowercase words separated by underscores", slug)
	}
	storeDir := filepath.Join(root, "internal", "store")
	if _, err := os.Stat(filepath.Join(storeDir, "migrations.go")); err != nil {
		return "", fmt.Errorf("this checkout has no timestamped migration runner: %w", err)
	}
	version := now.UnixMicro()
	if version > 1<<53-1 {
		return "", fmt.Errorf("migration id exceeds exact JSON integer limit %d, asked for %d", int64(1<<53-1), version)
	}
	stamp := strconv.FormatInt(version, 10)
	for _, source := range []struct{ dir, prefix, suffix string }{
		{filepath.Join(storeDir, "migrations"), stamp + "_", ".sql"},
		{storeDir, "migration_" + stamp + "_", ".go"},
	} {
		entries, err := os.ReadDir(source.dir)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), source.prefix) && strings.HasSuffix(entry.Name(), source.suffix) {
				return "", fmt.Errorf("migration id %s already belongs to %s; rerun to use the current UTC timestamp", stamp, filepath.Join(source.dir, entry.Name()))
			}
		}
	}
	file := filepath.Join("internal", "store", "migrations", stamp+"_"+slug+".sql")
	var content []byte
	if goMigration {
		file = filepath.Join("internal", "store", "migration_"+stamp+"_"+slug+".go")
		var err error
		content, err = format.Source([]byte(fmt.Sprintf(`package store

import (
 "database/sql"
 "errors"
)

func init() {
 registerMigration(migration{
  version: %s,
  desc: %q,
  apply: applyMigration%s,
 })
}

func applyMigration%s(tx *sql.Tx) error {
 return errors.New(%q)
}
`, stamp, strings.ReplaceAll(slug, "_", " "), stamp, stamp, "migration "+stamp+" "+strings.ReplaceAll(slug, "_", " ")+" is not implemented")))
		if err != nil {
			return "", err
		}
	}
	f, err := os.OpenFile(filepath.Join(root, file), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return "", fmt.Errorf("creating %s without overwriting an existing file: %w", file, err)
	}
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", fmt.Errorf("writing %s: %w", file, err)
	}
	return filepath.Join(root, file), nil
}
