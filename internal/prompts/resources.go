package prompts

import (
	"io"
	"io/fs"
	"strings"
)

type skillFS struct{ fs.FS }

func (s skillFS) Open(name string) (fs.File, error) {
	if name == "attn_skill" {
		return s.FS.Open("content/skills/attn")
	}
	return s.FS.Open("content/skills/attn/" + strings.TrimPrefix(name, "attn_skill/"))
}

func (s skillFS) ReadFile(name string) ([]byte, error) {
	f, err := s.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func AttnSkillFiles() fs.ReadFileFS { return skillFS{content} }

func skillRecipient() Recipient {
	r := Recipient{ID: "attn-skill", Description: "Installed skill and references. Availability does not establish that the harness loaded them."}
	entries, err := fs.Glob(content, "content/skills/attn/references/*.md")
	if err != nil {
		panic(err)
	}
	r.Events = append(r.Events, On("available", "available_skill", "Installed for supported harnesses by internal/agent; the harness decides when to load it.", Document("attn-skill", "content/skills/attn/SKILL.md")))
	for _, source := range entries {
		name := strings.TrimSuffix(strings.TrimPrefix(source, "content/skills/attn/references/"), ".md")
		r.Events = append(r.Events, On(name, "reference", "Loaded on demand through attn skill show or the installed skill directory.", Document("attn-skill."+name, source)))
	}
	return r
}
