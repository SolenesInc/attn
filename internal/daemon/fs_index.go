package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const maxFsIndexEntries = 25000

func (d *Daemon) handleFsIndex(client *wsClient, requestID, rawRoot string, extensions []string) {
	var files []string
	var truncated bool
	root, err := d.resolveFsRoot(client, rawRoot)
	if err == nil {
		files, truncated, err = indexRoot(root, maxFsIndexEntries, extensions)
	}
	msg := protocol.FsIndexResultMessage{
		Event:     protocol.EventFsIndexResult,
		RequestID: requestID,
		Success:   err == nil,
		Root:      root,
		Files:     files,
		Truncated: truncated,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
		msg.Files = []string{}
	}
	d.sendToClient(client, msg)
}

func normalizeExtensions(extensions []string) []string {
	normalized := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		ext = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ext), ".")))
		if ext != "" {
			normalized = append(normalized, ext)
		}
	}
	return normalized
}

func matchesExtension(name string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return false
	}
	for _, want := range extensions {
		if ext == want {
			return true
		}
	}
	return false
}

func skippedPath(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if skippedDirName(segment) {
			return true
		}
	}
	return false
}

func skippedDirName(name string) bool {
	return name == ".git"
}

// The cap applies AFTER extension filtering, or unasked-for files exhaust it. The Stat is
// load-bearing: WalkDir's skip-and-continue makes a bad root a silent zero-file success.
func indexRoot(root string, cap int, extensions []string) ([]string, bool, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("root %s is not a directory", root)
	}
	wanted := normalizeExtensions(extensions)
	if files, truncated, ok := indexRootViaGit(root, cap, wanted); ok {
		return files, truncated, nil
	}
	return indexRootViaWalk(root, cap, wanted)
}

func indexRootViaGit(root string, cap int, extensions []string) (files []string, truncated bool, ok bool) {
	// -z: NUL-separated, so odd bytes come back verbatim, not git-quoted.
	out, err := git.Output(git.OpMetadata, root,
		"ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false, false
	}

	seen := make(map[string]struct{})
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || skippedPath(rel) || !matchesExtension(rel, extensions) {
			continue
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if len(files) >= cap {
			truncated = true
			break
		}
		seen[rel] = struct{}{}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, truncated, true
}

// Skips .git, node_modules and every non-regular entry — a FIFO or socket would be
// advertised as openable when fs_read rejects it. A walk error skips that entry/subtree.
func indexRootViaWalk(root string, cap int, extensions []string) ([]string, bool, error) {
	var files []string
	truncated := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skippedDirName(name) || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if !matchesExtension(name, extensions) {
			return nil
		}
		if len(files) >= cap {
			truncated = true
			return fs.SkipAll
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(files)
	return files, truncated, nil
}
