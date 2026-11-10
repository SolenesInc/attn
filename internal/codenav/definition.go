package codenav

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Definition says how to launch the language server for one language in one
// repository.
type Definition struct {
	Command    []string        `json:"command"`
	Extensions []string        `json:"extensions"`
	Roots      []string        `json:"roots,omitempty"`
	Settings   json.RawMessage `json:"settings,omitempty"`
	Prepare    *Prepare        `json:"prepare,omitempty"`
	// LanguageIDs names the LSP language of a file by its extension, for
	// servers that serve several; a missing extension uses the language.
	LanguageIDs map[string]string `json:"language_ids,omitempty"`
}

// Prepare runs before the server starts, and again before a start once a file
// its inputs match has changed.
type Prepare struct {
	Command []string `json:"command"`
	Inputs  []string `json:"inputs"`
}

var builtins = map[string]Definition{
	"go": {Command: []string{"gopls"}, Extensions: []string{".go"}, Roots: []string{"go.work", "go.mod"}},
	"typescript": {
		Command:     []string{"typescript-language-server", "--stdio"},
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"},
		Roots:       []string{"tsconfig.json", "jsconfig.json", "package.json"},
		LanguageIDs: map[string]string{".tsx": "typescriptreact", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascriptreact"},
	},
	"rust":   {Command: []string{"rust-analyzer"}, Extensions: []string{".rs"}, Roots: []string{"Cargo.toml"}},
	"python": {Command: []string{"pyright-langserver", "--stdio"}, Extensions: []string{".py", ".pyi"}, Roots: []string{"pyproject.toml", "setup.py", "setup.cfg"}},
}

// Builtins are the languages attn can navigate with no setting, by name.
func Builtins() map[string]Definition { return builtins }

// ParseDefinition reads a definition as stored in its setting, and names each
// problem with the fix.
func ParseDefinition(value string) (Definition, error) {
	var def Definition
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&def); err != nil {
		return Definition{}, fmt.Errorf("a server definition is JSON like {\"command\": [\"gopls\"], \"extensions\": [\".go\"]}: %w", err)
	}
	if len(def.Command) == 0 || strings.TrimSpace(def.Command[0]) == "" {
		return Definition{}, errors.New("a server definition needs a command: the server's program and its arguments, as a list")
	}
	if len(def.Extensions) == 0 {
		return Definition{}, errors.New("a server definition needs extensions: the file extensions it serves, such as [\".java\"]")
	}
	for _, ext := range def.Extensions {
		if !strings.HasPrefix(ext, ".") {
			return Definition{}, fmt.Errorf("extension %q must start with a dot, such as \".java\"", ext)
		}
	}
	if p := def.Prepare; p != nil {
		if len(p.Command) == 0 {
			return Definition{}, errors.New("prepare needs a command, run from the workspace folder before the server starts")
		}
		if len(p.Inputs) == 0 {
			return Definition{}, errors.New("prepare needs inputs: the files, as git pathspec globs such as \"**/BUILD.bazel\", whose change runs it again")
		}
	}
	return def, nil
}

func (def Definition) serves(path string) bool {
	return slices.Contains(def.Extensions, strings.ToLower(filepath.Ext(path)))
}

func (def Definition) languageID(language, path string) string {
	if id, ok := def.LanguageIDs[strings.ToLower(filepath.Ext(path))]; ok {
		return id
	}
	return language
}

// Definitions are the server definitions in force for one repository: the
// built-ins, each replaced by a setting for the same language.
type Definitions map[string]Definition

// LanguageOf is the language a file belongs to, or "" when no definition
// serves it.
func (defs Definitions) LanguageOf(path string) string {
	for _, language := range defs.languages() {
		if defs[language].serves(path) {
			return language
		}
	}
	return ""
}

func (defs Definitions) languages() []string {
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// workspaceFolder is the nearest folder above path, up to the repository,
// that holds one of the definition's root markers; else the repository.
func (def Definition) workspaceFolder(repository, path string) string {
	dir := filepath.Dir(path)
	for {
		for _, marker := range def.Roots {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		if dir == repository || !strings.HasPrefix(dir, repository+string(filepath.Separator)) {
			return repository
		}
		dir = filepath.Dir(dir)
	}
}

// Missing is a server whose program is not installed on this machine.
type Missing struct {
	Program string
	BinDir  string
}

func (m Missing) Error() string {
	return fmt.Sprintf("%s is not in %s or on PATH. Install it, or link it with `attn nav link <path-to-%s>`", m.Program, m.BinDir, m.Program)
}

// Locate finds a server's program: linked in binDir first, then on PATH.
func Locate(binDir, program string) (string, error) {
	if filepath.IsAbs(program) {
		if _, err := os.Stat(program); err != nil {
			return "", Missing{Program: program, BinDir: binDir}
		}
		return program, nil
	}
	linked := filepath.Join(binDir, program)
	if info, err := os.Stat(linked); err == nil && !info.IsDir() {
		return linked, nil
	}
	found, err := exec.LookPath(program)
	if err != nil {
		return "", Missing{Program: program, BinDir: binDir}
	}
	return found, nil
}
