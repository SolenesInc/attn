package fsdoc

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/notebook"
)

const MaxFileSize = 2 << 20

type Store struct {
	root string
	mu   sync.Mutex
}

func NewStore(root string) *Store {
	if root != "" {
		root = filepath.Clean(root)
	}
	return &Store{root: root}
}

func (s *Store) Root() string { return s.root }

type Entry struct {
	Path     string
	Name     string
	IsDir    bool
	Size     int64
	Modified string
}

type Conflict struct {
	CurrentHash string
}

type NotFoundError struct{ Path string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("fsdoc: %s not found", e.Path) }

func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

func (s *Store) List(dir string) ([]Entry, error) {
	rel, err := cleanRel(dir, true)
	if err != nil {
		return nil, err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(abs)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			if rel == "" {
				return []Entry{}, nil
			}
			return nil, &NotFoundError{Path: dir}
		}
		return nil, statErr
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("fsdoc: %q is not a directory", dir)
	}
	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, dirent := range dirents {
		name := dirent.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		childAbs := filepath.Join(abs, name)
		// The root is externally syncable, so a child can be a symlink pointing outside
		// it: without this skip, List would expose an outside file's name and size.
		if notebook.EnsureWithinResolvedRoot(s.root, childAbs) != nil {
			continue
		}
		childInfo, ierr := dirent.Info()
		if ierr != nil {
			continue
		}
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		e := Entry{Path: childRel, Name: name, IsDir: childInfo.IsDir()}
		if !childInfo.IsDir() {
			e.Size = childInfo.Size()
			e.Modified = childInfo.ModTime().UTC().Format(time.RFC3339)
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func (s *Store) Read(p string) (content []byte, hash string, err error) {
	return s.ReadWithLimit(p, MaxFileSize)
}

func (s *Store) ReadWithLimit(p string, maxBytes int64) (content []byte, hash string, err error) {
	rel, err := cleanRel(p, false)
	if err != nil {
		return nil, "", err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return nil, "", err
	}
	info, statErr := os.Lstat(abs)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, "", &NotFoundError{Path: p}
		}
		return nil, "", statErr
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("fsdoc: %q is not a regular file", p)
	}
	if info.Size() > maxBytes {
		return nil, "", fmt.Errorf("fsdoc: %q exceeds %d byte read cap", p, maxBytes)
	}
	content, err = os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", &NotFoundError{Path: p}
		}
		return nil, "", err
	}
	return content, notebook.Hash(content), nil
}

// An empty baseHash means create-only: it Conflicts if the file already exists. A
// non-empty one applies only if the on-disk hash still matches.
func (s *Store) Write(p string, content []byte, baseHash string) (newHash string, conflict *Conflict, err error) {
	rel, err := cleanRel(p, false)
	if err != nil {
		return "", nil, err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return "", nil, err
	}
	if int64(len(content)) > MaxFileSize {
		return "", nil, fmt.Errorf("fsdoc: content for %q exceeds %d bytes", p, MaxFileSize)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, statErr := os.ReadFile(abs)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return "", nil, statErr
	}
	if baseHash == "" {
		if exists {
			return "", &Conflict{CurrentHash: notebook.Hash(existing)}, nil
		}
	} else {
		if !exists {
			return "", &Conflict{CurrentHash: ""}, nil
		}
		if cur := notebook.Hash(existing); cur != baseHash {
			return "", &Conflict{CurrentHash: cur}, nil
		}
	}
	if err := writeAtomic(abs, content); err != nil {
		return "", nil, err
	}
	return notebook.Hash(content), nil, nil
}

// A path that escapes the root returns an error, so the caller leaves such a link
// unflagged rather than guessing; only a genuine absence is (false, nil).
func (s *Store) Exists(p string) (bool, error) {
	rel, err := cleanRel(p, false)
	if err != nil {
		return false, err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) Rename(from, to string) error {
	fromRel, err := cleanRel(from, false)
	if err != nil {
		return err
	}
	toRel, err := cleanRel(to, false)
	if err != nil {
		return err
	}
	fromAbs, err := s.abs(fromRel)
	if err != nil {
		return err
	}
	toAbs, err := s.abs(toRel)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Lstat(fromAbs)
	if os.IsNotExist(err) {
		return &NotFoundError{Path: from}
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fsdoc: %q is not a regular file", from)
	}
	if fromAbs == toAbs {
		return nil
	}
	if _, err := os.Lstat(toAbs); err == nil {
		return fmt.Errorf("fsdoc: %q already exists", to)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return err
	}
	return os.Rename(fromAbs, toAbs)
}

func (s *Store) Delete(p string) error {
	rel, err := cleanRel(p, false)
	if err != nil {
		return err
	}
	abs, err := s.abs(rel)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return &NotFoundError{Path: p}
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fsdoc: %q is not a regular file", p)
	}
	return os.Remove(abs)
}

func CleanPath(p string) (string, error) { return cleanRel(p, false) }

func cleanRel(p string, allowRoot bool) (string, error) {
	trimmed := strings.TrimSpace(p)
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(trimmed, "/")), "/")
	if rel == "" || rel == "." {
		if allowRoot {
			return "", nil
		}
		return "", fmt.Errorf("fsdoc: %q is the root, not a file", p)
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" {
			return "", fmt.Errorf("fsdoc: %q has an empty path segment", p)
		}
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("fsdoc: %q has a dotfile/dotdir segment", p)
		}
	}
	return rel, nil
}

func (s *Store) abs(rel string) (string, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if abs != s.root && !strings.HasPrefix(abs, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("fsdoc: %q escapes the root", rel)
	}
	if err := notebook.EnsureWithinResolvedRoot(s.root, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// The temp name is dot-prefixed so it falls outside CleanPath's trackable set: fsdoc has no
// extension filter, so a watcher would see the swap file's events as a change to a real path.
func writeAtomic(absPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(absPath), fmt.Sprintf(".%s.tmp.%d.%d", filepath.Base(absPath), os.Getpid(), time.Now().UnixNano()))
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
