// Package views holds the rules of kept views: the files a version keeps,
// what an export gives an agent, and what an edit from that export saves.
package views

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/viewlang"
	"github.com/victorarias/attn/internal/viewlang/catalog"
)

// ViewFile is the name a kept view's view file has in each of its versions.
const ViewFile = "view.md"

// MaxBytes bounds the files of one version, which reach the view's machine in
// one message (protocol.MaxSocketFrameBytes, 8 MiB, carries 6 MiB as base64).
// Measured 2026-11-28 on oma: the views agents made while building views hold
// at most 8.8 KB with their pictures, the transcript task's 200 clips are
// 71 KB of JSON Lines, and its largest recorded clip is 1.32 MiB.
const MaxBytes = 4 << 20

// Files is a version of a view: its view file and the files it names, by
// their path inside the view's folder.
type Files map[string][]byte

func (f Files) size() int {
	size := 0
	for _, content := range f {
		size += len(content)
	}
	return size
}

var namePattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`) })

// Refusal is why a view cannot be kept or saved, with the code a command
// refuses with.
type Refusal struct {
	Code     string
	Message  string
	Problems []protocol.ViewProblem
}

func (r *Refusal) Error() string { return r.Message }

func refuse(code, format string, args ...any) *Refusal {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, args...)}
}

const (
	CodeBadName     = "bad_name"
	CodeBuiltIn     = "built_in"
	CodeTooLarge    = "too_large"
	CodeHasProblems = "has_problems"
	CodeNotStamped  = "not_stamped"
	CodeOtherView   = "other_view"
	CodeStale       = "stale_export"
)

// BuiltIn says whether name is one of the views that ship with attn.
func BuiltIn(name string) bool { return slices.Contains(profiles.BuiltInViews, name) }

// CheckName refuses a name a new kept view cannot take.
func CheckName(name string) error {
	if !namePattern().MatchString(name) {
		return refuse(CodeBadName, "%q is not a view name: use lowercase letters, digits and dashes, starting with a letter, at most 64", name)
	}
	if BuiltIn(name) {
		return refuse(CodeBuiltIn, "%s is one of attn's own views; give the view another name", name)
	}
	return nil
}

// Kept is the first version of a view an agent made: its view file as attn
// saves it and the files it names, or the refusal that says what to fix.
func Kept(folder Files) (Files, error) {
	view, ok := folder[ViewFile]
	if !ok {
		return nil, refuse(CodeHasProblems, "the folder has no view.md; name its view file view.md")
	}
	checked := viewlang.Check(string(view))
	if len(checked.Problems) > 0 {
		return nil, problems(checked.Problems)
	}
	if _, stamped := viewlang.StampOf(string(view)); stamped {
		return nil, refuse(CodeOtherView, "the view file is an export of a kept view; save it with view_edit, or remove its first line to keep it as a new view")
	}
	kept := Files{}
	for file, content := range folder {
		kept[file] = content
	}
	kept[ViewFile] = []byte(viewlang.Saved(string(view), checked.Language))
	return kept, limit(kept)
}

// Edited is what an edit of the view name saves: the folder the agent edited
// from an export of version current, whose files kept holds, with the files
// that are not text and that the folder lacks but its view file still names
// carried over from kept.
func Edited(name string, current int, kept, folder Files, missing []string) (Files, error) {
	view, ok := folder[ViewFile]
	if !ok {
		return nil, refuse(CodeNotStamped, "the folder has no view file")
	}
	stamp, stamped := viewlang.StampOf(string(view))
	switch {
	case !stamped:
		return nil, refuse(CodeNotStamped, "the view file's first line is not an export's stamp")
	case stamp.View != name:
		return nil, refuse(CodeOtherView, "the folder is an export of %s, not of %s", stamp.View, name)
	case stamp.Version != current:
		return nil, refuse(CodeStale, "the folder was exported from version %d of %s, and its newest version is %d", stamp.Version, name, current)
	}
	checked := viewlang.Check(string(view))
	if len(checked.Problems) > 0 {
		return nil, problems(checked.Problems)
	}
	saved := Files{}
	for file, content := range folder {
		saved[file] = content
	}
	for _, file := range missing {
		if content, ok := kept[file]; ok && !text(content) {
			saved[file] = content
		}
	}
	saved[ViewFile] = []byte(viewlang.Saved(string(view), checked.Language))
	return saved, limit(saved)
}

func problems(found []protocol.ViewProblem) *Refusal {
	r := refuse(CodeHasProblems, "the view has %d problem(s)", len(found))
	r.Problems = found
	return r
}

func limit(files Files) error {
	if size := files.size(); size > MaxBytes {
		return refuse(CodeTooLarge, "the view's files hold %d bytes, more than the %d a view keeps", size, MaxBytes)
	}
	return nil
}

// Exported is what an agent is given to edit version of the view name: the
// text of each file, the view file stamped, and the names of the files that
// are not text, which an edit keeps.
func Exported(name string, version int, files Files) (map[string]string, []string) {
	texts := map[string]string{}
	var other []string
	for file, content := range files {
		switch {
		case file == ViewFile:
			texts[file] = viewlang.Stamped(string(content), catalog.Language, viewlang.Stamp{View: name, Version: version})
		case text(content):
			texts[file] = string(content)
		default:
			other = append(other, file)
		}
	}
	sort.Strings(other)
	return texts, other
}

func text(content []byte) bool {
	return utf8.Valid(content) && !bytes.ContainsRune(content, 0)
}
