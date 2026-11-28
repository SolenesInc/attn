package views

import (
	"embed"
	"path"
)

//go:embed builtin/*.md
var builtInFiles embed.FS

// Shipped is the view file attn ships as its view name.
func Shipped(name string) (Files, bool) {
	if !BuiltIn(name) {
		return nil, false
	}
	content, err := builtInFiles.ReadFile(path.Join("builtin", name+".md"))
	if err != nil {
		return nil, false
	}
	return Files{ViewFile: content}, true
}
