package notebook

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEnsureScaffold(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nb")
	s := NewStore(root)

	created, err := s.EnsureScaffold()
	if err != nil {
		t.Fatalf("EnsureScaffold: %v", err)
	}
	wantFiles := []string{
		"index.md", "log.md", "knowledge/index.md",
		"knowledge/projects/index.md", "knowledge/areas/index.md",
		"knowledge/resources/index.md", "knowledge/archive/index.md",
	}
	if !reflect.DeepEqual(created, wantFiles) {
		t.Fatalf("first EnsureScaffold created = %v, want all reserved files", created)
	}
	for _, rel := range wantFiles {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("scaffold file %s missing: %v", rel, err)
		}
	}
	for _, rel := range []string{"journal", "knowledge", "knowledge/projects", "knowledge/areas", "knowledge/resources", "knowledge/archive"} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || !info.IsDir() {
			t.Fatalf("scaffold dir %s missing: %v", rel, err)
		}
	}

	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = s.EnsureScaffold()
	if err != nil {
		t.Fatalf("second EnsureScaffold: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("second EnsureScaffold created = %v, want none", created)
	}
	got, _ := os.ReadFile(filepath.Join(root, "index.md"))
	if string(got) != "EDITED" {
		t.Fatalf("EnsureScaffold clobbered an existing file: %q", got)
	}
}

func TestEnsureScaffoldReturnsPartialWritesOnFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nb")
	for _, d := range []string{"journal", "knowledge/projects", "knowledge/areas", "knowledge/resources", "knowledge/archive"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(root, "knowledge", "archive")
	if err := os.Chmod(archive, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(archive, 0o755) })

	s := NewStore(root)
	created, err := s.EnsureScaffold()
	if err == nil {
		t.Skip("write into read-only knowledge/archive/ unexpectedly succeeded (likely running as root)")
	}
	wantPartial := []string{
		"index.md", "log.md", "knowledge/index.md",
		"knowledge/projects/index.md", "knowledge/areas/index.md",
		"knowledge/resources/index.md",
	}
	if !reflect.DeepEqual(created, wantPartial) {
		t.Fatalf("partial EnsureScaffold created = %v, want %v", created, wantPartial)
	}
}

func TestReadNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Read("knowledge/areas/missing.md"); !IsNotFound(err) {
		t.Fatalf("Read missing = %v, want NotFoundError", err)
	}
}

func TestWriteCreateAndConflict(t *testing.T) {
	s := NewStore(t.TempDir())
	content := []byte("---\ntype: note\n---\nhello\n")

	hash, conflict, err := s.Write("knowledge/areas/foo.md", content, "")
	if err != nil || conflict != nil {
		t.Fatalf("create: err=%v conflict=%v", err, conflict)
	}
	if hash != Hash(content) {
		t.Fatalf("create hash = %q, want %q", hash, Hash(content))
	}

	_, conflict, err = s.Write("knowledge/areas/foo.md", []byte("other"), "")
	if err != nil {
		t.Fatalf("create-conflict err: %v", err)
	}
	if conflict == nil || conflict.CurrentHash != Hash(content) {
		t.Fatalf("expected create conflict carrying current hash, got %#v", conflict)
	}
	got, _, _ := s.Read("knowledge/areas/foo.md")
	if string(got) != string(content) {
		t.Fatalf("create-conflict overwrote the file: %q", got)
	}
}

func TestWriteCASEdit(t *testing.T) {
	s := NewStore(t.TempDir())
	v1 := []byte("---\ntype: note\n---\nv1\n")
	h1, _, err := s.Write("knowledge/areas/foo.md", v1, "")
	if err != nil {
		t.Fatal(err)
	}

	v2 := []byte("---\ntype: note\n---\nv2\n")
	_, conflict, err := s.Write("knowledge/areas/foo.md", v2, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if conflict == nil || conflict.CurrentHash != h1 {
		t.Fatalf("stale-base write = conflict %#v, want current hash %q", conflict, h1)
	}

	h2, conflict, err := s.Write("knowledge/areas/foo.md", v2, h1)
	if err != nil || conflict != nil {
		t.Fatalf("CAS edit: err=%v conflict=%v", err, conflict)
	}
	got, gotHash, _ := s.Read("knowledge/areas/foo.md")
	if string(got) != string(v2) || gotHash != h2 {
		t.Fatalf("CAS edit did not apply: content=%q hash=%q", got, gotHash)
	}
}

func TestAppendJournal(t *testing.T) {
	s := NewStore(t.TempDir())

	rel, _, err := s.AppendJournal("2026-06-13", "first entry")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rel != "journal/2026-06-13.md" {
		t.Fatalf("journal path = %q", rel)
	}
	rel, _, err = s.AppendJournal("2026-06-13", "second entry")
	if err != nil {
		t.Fatalf("second append: %v", err)
	}

	content, _, _ := s.Read(rel)
	doc := ParsePermissive(content)
	if doc.Type() != TypeJournal {
		t.Fatalf("journal type = %q, want %q", doc.Type(), TypeJournal)
	}
	if !strings.Contains(doc.Body, "first entry") || !strings.Contains(doc.Body, "second entry") {
		t.Fatalf("journal body missing entries:\n%s", doc.Body)
	}

	if _, _, err := s.AppendJournal("not-a-date", "x"); err == nil {
		t.Fatal("AppendJournal should reject a malformed date")
	}
}

func TestAppendJournalEntryOnce(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)

	const marker = "<!-- attn:dispatch:abc -->"
	entry := "## 09:00 — Ship it (completed)\n\nShipped.\n\n" + marker

	rel, written, _, err := s.AppendJournalEntryOnce("2026-06-14", marker, entry)
	if err != nil || !written {
		t.Fatalf("first append: written=%v err=%v", written, err)
	}

	if _, written, _, err := NewStore(root).AppendJournalEntryOnce("2026-06-14", marker, entry); err != nil || written {
		t.Fatalf("duplicate append: written=%v err=%v (want written=false)", written, err)
	}

	const marker2 = "<!-- attn:dispatch:xyz -->"
	if _, written, _, err := s.AppendJournalEntryOnce("2026-06-14", marker2, "## 10:00 — Other (failed)\n\nBroke.\n\n"+marker2); err != nil || !written {
		t.Fatalf("distinct append: written=%v err=%v", written, err)
	}

	content, _, _ := s.Read(rel)
	body := string(content)
	if strings.Count(body, marker) != 1 {
		t.Fatalf("marker appears %d times, want 1:\n%s", strings.Count(body, marker), body)
	}
	if !strings.Contains(body, "Shipped.") || !strings.Contains(body, "Broke.") {
		t.Fatalf("journal missing expected entries:\n%s", body)
	}

	if _, _, _, err := s.AppendJournalEntryOnce("2026-06-14", "", "x"); err == nil {
		t.Fatal("AppendJournalEntryOnce should reject an empty marker")
	}
}

func TestAppendJournalEntryOnceMarkerPrefixDoesNotCollide(t *testing.T) {
	s := NewStore(t.TempDir())
	const m10 = "<!-- attn:dispatch:dsp-10 -->"
	const m1 = "<!-- attn:dispatch:dsp-1 -->"

	if _, written, _, err := s.AppendJournalEntryOnce("2026-06-14", m10, "## dsp-10\n\nten.\n\n"+m10); err != nil || !written {
		t.Fatalf("append dsp-10: written=%v err=%v", written, err)
	}
	if _, written, _, err := s.AppendJournalEntryOnce("2026-06-14", m1, "## dsp-1\n\none.\n\n"+m1); err != nil || !written {
		t.Fatalf("append dsp-1 after dsp-10: written=%v err=%v (delimiter collision?)", written, err)
	}
}

// Run under `go test -race ./internal/notebook` to also catch a regression in appendToNoteOnce's single lock.
func TestAppendJournalEntryOnceConcurrent(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	const marker = "<!-- attn:dispatch:race -->"
	entry := "## 09:00 — Race (completed)\n\nRan.\n\n" + marker

	const n = 16
	var wins int64
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			if _, written, _, err := s.AppendJournalEntryOnce("2026-06-14", marker, entry); err == nil && written {
				atomic.AddInt64(&wins, 1)
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("concurrent writers that reported written=true: %d, want 1", wins)
	}
	rel, _, _, _ := s.AppendJournalEntryOnce("2026-06-14", marker, entry)
	content, _, _ := s.Read(rel)
	if got := strings.Count(string(content), marker); got != 1 {
		t.Fatalf("marker appears %d times after %d concurrent writers, want 1", got, n)
	}
}

func TestAppendInbox(t *testing.T) {
	s := NewStore(t.TempDir())

	rel, _, err := s.AppendInbox("first message")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if rel != FileInbox {
		t.Fatalf("inbox path = %q, want %q", rel, FileInbox)
	}
	if _, _, err = s.AppendInbox("second message"); err != nil {
		t.Fatalf("second append: %v", err)
	}

	content, _, _ := s.Read(FileInbox)
	doc := ParsePermissive(content)
	if !strings.Contains(doc.Body, "Chief inbox") ||
		!strings.Contains(doc.Body, "first message") || !strings.Contains(doc.Body, "second message") {
		t.Fatalf("inbox body missing header/entries:\n%s", doc.Body)
	}
	if strings.Index(doc.Body, "first message") > strings.Index(doc.Body, "second message") {
		t.Fatalf("inbox entries out of order:\n%s", doc.Body)
	}

	if _, _, err := s.AppendInbox("   "); err == nil {
		t.Fatal("AppendInbox should reject an empty entry")
	}
}

func TestStoreRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "nb")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("TOP SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)

	if _, _, err := s.Read("evil/secret.md"); err == nil {
		t.Fatal("Read through a symlinked dir should be rejected")
	}
	if _, _, err := s.Write("evil/secret.md", []byte("---\ntype: note\n---\nx\n"), ""); err == nil {
		t.Fatal("Write through a symlinked dir should be rejected")
	}
	if got, _ := os.ReadFile(secret); string(got) != "TOP SECRET\n" {
		t.Fatalf("outside file was modified through the symlink: %q", got)
	}
}

func TestStoreAllowsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s := NewStore(link)
	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatalf("EnsureScaffold on a symlinked root: %v", err)
	}
	if _, _, err := s.Write("knowledge/areas/foo.md", []byte("---\ntype: note\n---\nx\n"), ""); err != nil {
		t.Fatalf("write under a symlinked root should work: %v", err)
	}
}

func TestListPrefixIsPathSegmentBoundary(t *testing.T) {
	s := NewStore(t.TempDir())
	body := []byte("---\ntype: note\n---\nx\n")
	if _, _, err := s.Write("knowledge/areas/foo.md", body, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Write("knowledge/areas-archive/bar.md", body, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List("knowledge/areas")
	if err != nil {
		t.Fatal(err)
	}
	foundFoo, foundArchive := false, false
	for _, e := range entries {
		switch e.Path {
		case "knowledge/areas/foo.md":
			foundFoo = true
		case "knowledge/areas-archive/bar.md":
			foundArchive = true
		}
	}
	if !foundFoo {
		t.Fatalf("prefix should include knowledge/areas/foo.md; got %+v", entries)
	}
	if foundArchive {
		t.Fatalf("prefix must NOT leak the sibling knowledge/areas-archive/bar.md; got %+v", entries)
	}
}

func TestListReadsFrontmatterFromLargeFile(t *testing.T) {
	s := NewStore(t.TempDir())
	big := "---\ntype: note\n---\n# Big\n" + strings.Repeat("x\n", 100<<10)
	if _, _, err := s.Write("knowledge/areas/big.md", []byte(big), ""); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List("knowledge/areas/big.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != "note" || entries[0].Title != "Big" {
		t.Fatalf("List did not extract frontmatter from a large file: %+v", entries)
	}
	if entries[0].Size != int64(len(big)) {
		t.Fatalf("List size = %d, want %d", entries[0].Size, len(big))
	}
}

func TestAppendJournalPreservesExistingFrontmatter(t *testing.T) {
	s := NewStore(t.TempDir())
	existing := "---\ntype: journal\n# external note\nobsidian_id: 007\nzeta: z\ntitle: jrnl\n---\n# entries\n\nfirst\n"
	if _, _, err := s.Write("journal/2026-06-13.md", []byte(existing), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendJournal("2026-06-13", "second"); err != nil {
		t.Fatal(err)
	}
	content, _, _ := s.Read("journal/2026-06-13.md")
	got := string(content)
	for _, want := range []string{"# external note", "obsidian_id: 007", "first", "second"} {
		if !strings.Contains(got, want) {
			t.Fatalf("append dropped %q:\n%s", want, got)
		}
	}
}

func TestList(t *testing.T) {
	s := NewStore(t.TempDir())

	entries, err := s.List("")
	if err != nil {
		t.Fatalf("List uninitialized: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("List uninitialized = %d entries, want 0", len(entries))
	}

	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Write("knowledge/areas/foo.md", []byte("---\ntype: note\nsummary: a decision\n---\n# Foo\n\nbody\n"), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root(), ".attn", "raw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root(), ".attn", "raw", "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err = s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	paths := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Path, ".attn/") {
			t.Fatalf("List surfaced machine state: %q", e.Path)
		}
		paths[e.Path] = e
	}
	foo, ok := paths["knowledge/areas/foo.md"]
	if !ok {
		t.Fatalf("List missing the written note; got %v", entries)
	}
	if foo.Type != "note" || foo.Title != "Foo" || foo.Summary != "a decision" {
		t.Fatalf("List entry metadata = %+v", foo)
	}

	mem, err := s.List("/knowledge/areas")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range mem {
		if !strings.HasPrefix(e.Path, "knowledge/areas") {
			t.Fatalf("prefixed List returned out-of-subtree entry %q", e.Path)
		}
	}
	if len(mem) == 0 {
		t.Fatal("prefixed List returned nothing")
	}
}

func TestBacklinks(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, s, "knowledge/areas/target.md", "---\ntype: note\n---\n# Target\n\nthe decision\n")
	mustWrite(t, s, "knowledge/areas/linker.md", "---\ntype: note\n---\n# Linker\n\nsee [the call](/knowledge/areas/target.md#why) for context\n")
	mustWrite(t, s, "journal/2026-06-13.md", "---\ntype: journal\n---\nfollowed [target](/knowledge/areas/target.md) today\n")
	mustWrite(t, s, "knowledge/resources/unrelated.md", "---\ntype: note\n---\nlinks [elsewhere](/knowledge/areas/other.md) only\n")
	mustWrite(t, s, "knowledge/areas/target.md", "---\ntype: note\n---\n# Target\n\nthe decision; see [self](/knowledge/areas/target.md)\n")

	got, err := s.Backlinks("/knowledge/areas/target.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	gotPaths := make([]string, len(got))
	for i, e := range got {
		gotPaths[i] = e.Path
	}
	want := []string{"journal/2026-06-13.md", "knowledge/areas/linker.md"}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("Backlinks paths = %v, want %v", gotPaths, want)
	}
	for _, e := range got {
		if e.Path == "knowledge/areas/linker.md" && e.Title != "Linker" {
			t.Fatalf("backlink entry lost metadata: %+v", e)
		}
	}

	dangling, err := s.Backlinks("/knowledge/areas/other.md")
	if err != nil {
		t.Fatalf("Backlinks(dangling): %v", err)
	}
	if len(dangling) != 1 || dangling[0].Path != "knowledge/resources/unrelated.md" {
		t.Fatalf("dangling Backlinks = %v, want [knowledge/resources/unrelated.md]", dangling)
	}

	none, err := s.Backlinks("/journal/2026-06-13.md")
	if err != nil {
		t.Fatalf("Backlinks(none): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("Backlinks(none) = %v, want empty", none)
	}
}

func TestBacklinksSkipsOversizedExternalFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, s, "knowledge/areas/linker.md", "---\ntype: note\n---\nsee [the call](/knowledge/areas/target.md) here\n")

	big := append([]byte("---\ntype: note\n---\nlinks [target](/knowledge/areas/target.md)\n"), make([]byte, MaxFileSize+1)...)
	bigPath := filepath.Join(dir, "knowledge", "areas", "oversized.md")
	if err := os.WriteFile(bigPath, big, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := s.Backlinks("/knowledge/areas/target.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	gotPaths := make([]string, len(got))
	for i, e := range got {
		gotPaths[i] = e.Path
	}
	want := []string{"knowledge/areas/linker.md"}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("Backlinks skipped/included the wrong files: got %v, want %v", gotPaths, want)
	}
}

func mustWrite(t *testing.T, s *Store, relPath, content string) {
	t.Helper()
	// A create-only write against an existing path returns a non-nil Conflict with a nil error, so retrying on err alone silently no-ops.
	_, conflict, err := s.Write(relPath, []byte(content), "")
	if err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	if conflict != nil {
		if _, _, werr := s.Write(relPath, []byte(content), conflict.CurrentHash); werr != nil {
			t.Fatalf("rewrite %s: %v", relPath, werr)
		}
	}
}

func TestNewStoreNormalizesTrailingSlashRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nb")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root + string(filepath.Separator))
	if got := s.Root(); got != root {
		t.Fatalf("Root() = %q, want cleaned %q", got, root)
	}
	if _, _, err := s.Write("index.md", []byte("# hi\n"), ""); err != nil {
		t.Fatalf("Write under a trailing-slash root: %v", err)
	}
	content, _, err := s.Read("index.md")
	if err != nil {
		t.Fatalf("Read under a trailing-slash root: %v", err)
	}
	if string(content) != "# hi\n" {
		t.Fatalf("round-trip content = %q", content)
	}
}

func TestListSkipsSymlinkResolvingOutsideRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nb")
	s := NewStore(root)
	if _, _, err := s.Write("real.md", []byte("---\ntype: note\ntitle: Real\n---\n# Real\n"), ""); err != nil {
		t.Fatalf("seed real note: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("---\ntype: note\ntitle: Secret\n---\nclassified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.md")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	entries, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawReal bool
	for _, e := range entries {
		if e.Path == "linked.md" || e.Title == "Secret" {
			t.Fatalf("List exposed an out-of-root symlink: %+v", e)
		}
		if e.Path == "real.md" {
			sawReal = true
		}
	}
	if !sawReal {
		t.Fatalf("List dropped the legitimate in-root note; entries=%+v", entries)
	}
}
