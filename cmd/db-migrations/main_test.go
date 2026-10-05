package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var commandBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-db-migrations-cli-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	commandBinary = filepath.Join(dir, "db-migrations")
	build := exec.Command("go", "build", "-o", commandBinary, ".")
	if data, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building CLI: %v\n%s", err, data)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Migration Tests", "-c", "user.email=migrations@example.test", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + filepath.Join(root, "no-hooks")}, args...)...)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, data)
	}
	return strings.TrimSpace(string(data))
}

func writeTestFile(t *testing.T, root, path, data string) {
	t.Helper()
	destination := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func migrationRepository(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "init", "--quiet", "--initial-branch=next")
	writeTestFile(t, root, "internal/store/migrations.go", "package store\nconst lastLegacyVersion = 171\n")
	if err := os.MkdirAll(filepath.Join(root, "internal/store/migrations"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func cli(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command(commandBinary, args...)
	command.Dir = root
	data, err := command.CombinedOutput()
	return strings.TrimSpace(string(data)), err
}

func TestCLICreatesSQLAndRegisteredGoMigrations(t *testing.T) {
	for _, goMigration := range []bool{false, true} {
		t.Run(fmt.Sprint(goMigration), func(t *testing.T) {
			root := migrationRepository(t)
			subdir := filepath.Join(root, "working-directory")
			if err := os.Mkdir(subdir, 0755); err != nil {
				t.Fatal(err)
			}
			args := []string{"new"}
			if goMigration {
				args = append(args, "--go")
			}
			args = append(args, "session_priority")
			before := time.Now().UTC().Truncate(time.Microsecond)
			file, err := cli(t, subdir, args...)
			after := time.Now().UTC()
			if err != nil {
				t.Fatalf("new: %v\n%s", err, file)
			}
			pattern := regexp.MustCompile(`^(?:migration_)?([0-9]{16})_session_priority\.(?:sql|go)$`)
			parts := pattern.FindStringSubmatch(filepath.Base(file))
			if parts == nil {
				t.Fatalf("generated filename %s", file)
			}
			version, err := strconv.ParseInt(parts[1], 10, 64)
			stamp := time.UnixMicro(version)
			if err != nil || stamp.Before(before) || stamp.After(after) {
				t.Fatalf("timestamp %s outside %s..%s: %v", parts[1], before, after, err)
			}
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if goMigration {
				if filepath.Dir(file) != filepath.Join(root, "internal/store") || !strings.Contains(string(content), "registerMigration(migration{") || !strings.Contains(string(content), parts[1]) || !strings.Contains(string(content), `"session priority"`) || !strings.Contains(string(content), "is not implemented") {
					t.Fatalf("Go scaffold %s:\n%s", file, content)
				}
			} else {
				if filepath.Dir(file) != filepath.Join(root, "internal/store/migrations") || len(content) != 0 {
					t.Fatalf("SQL scaffold %s:\n%s", file, content)
				}
				if output, err := cli(t, root, "--help"); err != nil {
					t.Fatalf("CLI cannot run with its empty SQL scaffold: %v\n%s", err, output)
				}
			}
		})
	}
}

func TestCLIRejectsInvalidSlugs(t *testing.T) {
	for _, slug := range []string{"", "../escape", "UpperCase", "has-hyphens", "has space", "leading__gap", "_prefix"} {
		t.Run(slug, func(t *testing.T) {
			root := migrationRepository(t)
			output, err := cli(t, root, "new", slug)
			if err == nil || !strings.Contains(output, "invalid slug") {
				t.Fatalf("new %q = %v, %s", slug, err, output)
			}
			entries, err := os.ReadDir(filepath.Join(root, "internal/store/migrations"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid slug wrote files: %v (%v)", entries, err)
			}
		})
	}
}

func TestCreationRejectsExistingTimestampAcrossBothFormats(t *testing.T) {
	now := time.Date(2026, 10, 6, 14, 34, 56, 0, time.FixedZone("UTC+2", 2*60*60))
	for _, existing := range []string{"internal/store/migrations/1791290096000000_existing.sql", "internal/store/migration_1791290096000000_existing.go"} {
		for _, goMigration := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", existing, goMigration), func(t *testing.T) {
				root := migrationRepository(t)
				writeTestFile(t, root, existing, "existing migration stays intact\n")
				args := []string{"new"}
				if goMigration {
					args = append(args, "--go")
				}
				args = append(args, "different_slug")
				var output bytes.Buffer
				err := run(args, root, now, &output, &output)
				if err == nil || !strings.Contains(err.Error(), "1791290096000000") || !strings.Contains(err.Error(), filepath.Base(existing)) {
					t.Fatalf("collision = %v", err)
				}
				data, err := os.ReadFile(filepath.Join(root, existing))
				if err != nil || string(data) != "existing migration stays intact\n" {
					t.Fatalf("existing file changed: %s (%v)", data, err)
				}
				paths, err := filepath.Glob(filepath.Join(root, "internal/store/migrations/*different_slug*"))
				if err != nil || len(paths) != 0 {
					t.Fatalf("collision created SQL migration: %v (%v)", paths, err)
				}
				paths, err = filepath.Glob(filepath.Join(root, "internal/store/migration_*different_slug*"))
				if err != nil || len(paths) != 0 {
					t.Fatalf("collision created Go migration: %v (%v)", paths, err)
				}
			})
		}
	}
}

const legacyFixture = `package store
const baseSchema = "CREATE TABLE initial (id INTEGER)"
const legacyMigrationSQL = "ALTER TABLE initial ADD COLUMN first TEXT"
const migrationBodySQL = "UPDATE initial SET first = 'original'"
var retiredColumns = []string{"old_column"}
type migration struct { version int; desc, sql string; apply func() error }
var migrations = []migration{
 {1, "first", legacyMigrationSQL, applyMigration1},
 {2, "second", "", func() error { return applyMigration2() }},
}
func applyMigration1() error { return executeSQL(migrationBodySQL + legacyRecord{}.Value) }
func applyMigration2() error { return executeSQL(retiredColumns[0]) }
func executeSQL(statement string) error { return nil }
func connectionCode() error { return nil }
type unrelatedRecord struct { Value string }
` + "\ntype legacyRecord struct { Value string; Nested legacyPayload }\ntype legacyPayload struct { Value string `json:\"value\"` }\n"

func TestCLIEnforcesMergedDefinitionsWithoutFreezingOtherStoreCode(t *testing.T) {
	sqlPath := "internal/store/migrations/20261005000000_original.sql"
	goPath := "internal/store/migration_20261005000100_original.go"
	cases := []struct {
		name    string
		change  func(t *testing.T, root string)
		refusal string
	}{
		{"unchanged", func(*testing.T, string) {}, ""},
		{"new SQL", func(t *testing.T, root string) {
			file, err := cli(t, root, "new", "new_column")
			if err != nil {
				t.Fatalf("new: %v %s", err, file)
			}
			if err := os.WriteFile(file, []byte("ALTER TABLE initial ADD COLUMN next TEXT;\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}, ""},
		{"new Go", func(t *testing.T, root string) {
			if output, err := cli(t, root, "new", "--go", "new_backfill"); err != nil {
				t.Fatalf("new: %v %s", err, output)
			}
		}, ""},
		{"edit SQL", func(t *testing.T, root string) { writeTestFile(t, root, sqlPath, "SELECT 2;\n") }, sqlPath},
		{"delete SQL", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, sqlPath)); err != nil {
				t.Fatal(err)
			}
		}, sqlPath},
		{"rename SQL", func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, sqlPath), filepath.Join(root, "internal/store/migrations/20261005000000_renamed.sql")); err != nil {
				t.Fatal(err)
			}
		}, sqlPath},
		{"edit Go", func(t *testing.T, root string) { writeTestFile(t, root, goPath, "package store\nfunc changed() {}\n") }, goPath},
		{"delete Go", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, goPath)); err != nil {
				t.Fatal(err)
			}
		}, goPath},
		{"ladder", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, `{1, "first"`, `{1, "changed"`, 1))
		}, "migrations"},
		{"legacy SQL", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, "ADD COLUMN first TEXT", "ADD COLUMN changed TEXT", 1))
		}, "legacyMigrationSQL"},
		{"Go body SQL", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, "'original'", "'changed'", 1))
		}, "migrationBodySQL"},
		{"Go body", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, "func applyMigration2() error { return executeSQL(retiredColumns[0]) }", `func applyMigration2() error { return executeSQL("changed") }`, 1))
		}, "applyMigration2"},
		{"Go variable", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, `"old_column"`, `"changed_column"`, 1))
		}, "retiredColumns"},
		{"reachable helper", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, "func executeSQL(statement string) error { return nil }", `func executeSQL(statement string) error { return executeSQL("changed helper") }`, 1))
		}, "executeSQL"},
		{"reachable type", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, "version int; desc", "version int64; desc", 1))
		}, "migration"},
		{"nested JSON type", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, `json:"value"`, `json:"changed"`, 1))
		}, "legacyPayload"},
		{"unrelated type", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, "type unrelatedRecord struct { Value string }", "type unrelatedRecord struct { Value int }", 1))
		}, ""},
		{"legacy boundary", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/migrations.go", "package store\nconst lastLegacyVersion = 172\n")
		}, "lastLegacyVersion"},
		{"connection code", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(legacyFixture, "func connectionCode() error { return nil }", `func connectionCode() error { return executeSQL("new connection code") }`, 1))
		}, ""},
		{"formatting", func(t *testing.T, root string) {
			writeTestFile(t, root, "internal/store/sqlite.go", "\n// changed formatting\n"+strings.ReplaceAll(legacyFixture, "return nil", "return   nil"))
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := migrationRepository(t)
			writeTestFile(t, root, "internal/store/sqlite.go", legacyFixture)
			writeTestFile(t, root, sqlPath, "SELECT 1;\n")
			writeTestFile(t, root, goPath, "package store\nfunc originalDataMigration() {}\n")
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "--quiet", "-m", "merged baseline")
			base := gitTest(t, root, "rev-parse", "HEAD")
			c.change(t, root)
			output, err := cli(t, root, "check-history", "--base", base)
			if c.refusal != "" {
				if err == nil || !strings.Contains(output, c.refusal) {
					t.Fatalf("want refusal %s, got %v\n%s", c.refusal, err, output)
				}
			} else if err != nil {
				t.Fatalf("history: %v\n%s", err, output)
			}
		})
	}
}

func TestCLIReportsBootstrapAndMissingHistory(t *testing.T) {
	root := migrationRepository(t)
	if err := os.Remove(filepath.Join(root, "internal/store/migrations.go")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "internal/store/sqlite.go", legacyFixture)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "--quiet", "-m", "before timestamp runner")
	base := gitTest(t, root, "rev-parse", "HEAD")
	output, err := cli(t, root, "check-history", "--base", base)
	if err != nil || !strings.Contains(output, "freeze starts") {
		t.Fatalf("bootstrap = %v %s", err, output)
	}
	output, err = cli(t, root, "check-history", "--base", "missing-base")
	if err == nil || !strings.Contains(output, "missing-base") {
		t.Fatalf("missing base = %v %s", err, output)
	}
	writeTestFile(t, root, "internal/store/migrations.go", "package store\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "--quiet", "-m", "invalid frozen baseline")
	base = gitTest(t, root, "rev-parse", "HEAD")
	output, err = cli(t, root, "check-history", "--base", base)
	if err == nil || !strings.Contains(output, "missing frozen legacy definition lastLegacyVersion") {
		t.Fatalf("missing definition = %v %s", err, output)
	}
}

func TestCLILeavesUnrelatedFunctionsNamedLikeSelectorsOrLocalsEditable(t *testing.T) {
	for _, body := range []string{
		`func applyMigration2() error { return fmt.Errorf("failure") }`,
		`func applyMigration2() error { Errorf := func(string) error { return nil }; return Errorf("local") }`,
	} {
		t.Run(body, func(t *testing.T) {
			root := migrationRepository(t)
			source := strings.Replace(legacyFixture, "package store", "package store\nimport \"fmt\"", 1)
			source = strings.Replace(source, "func applyMigration2() error { return executeSQL(retiredColumns[0]) }", body, 1)
			source += "\nfunc Errorf(value string) error { return nil }\n"
			writeTestFile(t, root, "internal/store/sqlite.go", source)
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "--quiet", "-m", "merged definitions")
			base := gitTest(t, root, "rev-parse", "HEAD")
			writeTestFile(t, root, "internal/store/sqlite.go", strings.Replace(source, "func Errorf(value string) error { return nil }", `func Errorf(value string) error { return fmt.Errorf("updated unrelated function") }`, 1))
			if output, err := cli(t, root, "check-history", "--base", base); err != nil {
				t.Fatalf("unrelated function froze: %v\n%s", err, output)
			}
		})
	}
}

func TestCreationUsesDistinctIDsWithinOneSecond(t *testing.T) {
	root := migrationRepository(t)
	now := time.Date(2026, 10, 6, 12, 34, 56, 123456000, time.UTC)
	var first, second bytes.Buffer
	if err := run([]string{"new", "first"}, root, now, &first, &first); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"new", "second"}, root, now.Add(time.Microsecond), &second, &second); err != nil {
		t.Fatal(err)
	}
	firstID := strings.Split(filepath.Base(strings.TrimSpace(first.String())), "_")[0]
	secondID := strings.Split(filepath.Base(strings.TrimSpace(second.String())), "_")[0]
	if firstID == secondID {
		t.Fatalf("one-microsecond difference reused id %s", firstID)
	}
	if firstID != strconv.FormatInt(now.UnixMicro(), 10) || secondID != strconv.FormatInt(now.Add(time.Microsecond).UnixMicro(), 10) {
		t.Fatalf("ids = %s, %s", firstID, secondID)
	}
}
