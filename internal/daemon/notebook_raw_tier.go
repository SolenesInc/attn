package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
)

// Ids are CLIENT-CONTROLLED, and a ".." id would climb out and overwrite the curated journal.
func rawTierFilename(id string) (string, error) {
	return rawTierName(id, ".md")
}

func rawTierSegment(id string) (string, error) {
	return rawTierName(id, "")
}

func rawTierName(id, suffix string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("raw-tier id is empty")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." || strings.HasPrefix(id, ".") {
		return "", fmt.Errorf("raw-tier id %q is not a single safe path segment", id)
	}
	// Control chars rejected: an id is interpolated into the plaintext "source:" footer, so a newline could inject a forged grounding line.
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("raw-tier id %q contains a control character", id)
		}
	}
	name := id + suffix
	if filepath.Base(name) != name || name != filepath.Clean(name) {
		return "", fmt.Errorf("raw-tier id %q does not produce a safe filename", id)
	}
	return name, nil
}

// The single chokepoint that validates the id and asserts containment again.
func writeRawAtomic(root, dir, id string, content []byte) error {
	name, err := rawTierFilename(id)
	if err != nil {
		return err
	}
	absPath := filepath.Join(dir, name)
	cleanDir := filepath.Clean(dir)
	if filepath.Dir(absPath) != cleanDir {
		return fmt.Errorf("raw-tier write for %q escapes %q", id, dir)
	}
	// The root is externally syncable, so an ancestor could be a symlink out of it.
	if err := notebook.EnsureWithinResolvedRoot(root, absPath); err != nil {
		return err
	}
	if err := os.MkdirAll(cleanDir, 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", absPath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, absPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// MUST run at every removal site AFTER the keeper compaction cancel/forget and BEFORE
// store.RemoveWorkspace, whose DELETE an async writer cannot win.
func (d *Daemon) snapshotWorkspaceContextOnRemove(id, title string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}

	canonical, err := d.store.GetWorkspaceContext(id)
	if err != nil {
		d.logf("context snapshot %s (%s): read context: %v", id, title, err)
		return
	}
	if strings.TrimSpace(canonical.Content) == "" || canonical.Revision == 0 {
		return
	}

	root, err := d.notebookRoot()
	if err != nil {
		d.logf("context snapshot %s (%s): notebook root unavailable: %v", id, title, err)
		return
	}
	if strings.TrimSpace(root) == "" {
		return
	}

	// Neutralize BEFORE appending the genuine footer, so free text cannot forge a journal marker.
	var doc strings.Builder
	doc.WriteString(neutralizeJournalMarkers(canonical.Content))
	fmt.Fprintf(&doc, "\nsource: workspace-context:%s@%d\n", id, canonical.Revision)

	dir := notebook.RawContextSnapshotsDir(root)
	if err := writeRawAtomic(root, dir, id, []byte(doc.String())); err != nil {
		d.logf("context snapshot %s (%s): write under %s: %v", id, title, dir, err)
		return
	}
}

func neutralizeJournalMarkers(s string) string {
	return strings.ReplaceAll(s, "<!--", "<! --")
}
