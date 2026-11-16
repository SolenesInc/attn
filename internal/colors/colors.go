package colors

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
)

//go:embed schemes/*.json
var schemeFiles embed.FS

const defaultID = "attn"

type Scheme struct {
	ID    string     `json:"-"`
	Name  string     `json:"name"`
	Dark  Appearance `json:"dark"`
	Light Appearance `json:"light"`
}

type Appearance struct {
	UI       map[string]string `json:"ui"`
	Terminal map[string]string `json:"terminal"`
}

type colorFormat struct {
	pattern *regexp.Regexp
	spelled string
}

var colorFormats = map[string]colorFormat{
	"ui":       {regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`), "#rrggbb or #rrggbbaa"},
	"terminal": {regexp.MustCompile(`^#[0-9a-fA-F]{6}$`), "#rrggbb"},
}

var embedded = sync.OnceValue(func() []Scheme {
	entries, err := schemeFiles.ReadDir("schemes")
	if err != nil {
		panic(err)
	}
	schemes := make([]Scheme, 0, len(entries))
	for _, entry := range entries {
		data, err := schemeFiles.ReadFile("schemes/" + entry.Name())
		if err != nil {
			panic(err)
		}
		var scheme Scheme
		if err := json.Unmarshal(data, &scheme); err != nil {
			panic(fmt.Sprintf("color scheme %s: %v", entry.Name(), err))
		}
		scheme.ID = strings.TrimSuffix(entry.Name(), path.Ext(entry.Name()))
		schemes = append(schemes, scheme)
	}
	slices.SortFunc(schemes, func(a, b Scheme) int {
		switch {
		case a.ID == defaultID:
			return -1
		case b.ID == defaultID:
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return schemes
})

func Schemes() []Scheme {
	return embedded()
}

func Find(id string) (Scheme, bool) {
	for _, scheme := range embedded() {
		if scheme.ID == id {
			return scheme, true
		}
	}
	return Scheme{}, false
}

func CheckScheme(id string) error {
	if id == "" {
		return nil
	}
	if _, ok := Find(id); ok {
		return nil
	}
	ids := make([]string, 0, len(embedded()))
	for _, scheme := range embedded() {
		ids = append(ids, scheme.ID)
	}
	return fmt.Errorf("%q is not a color scheme; schemes: %s", id, strings.Join(ids, ", "))
}

func CheckOverrides(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var override map[string]any
	if err := json.Unmarshal([]byte(value), &override); err != nil {
		return fmt.Errorf(`not a JSON object like {"dark":{"ui":{"bg_app":"#24273a"}}}: %v`, err)
	}
	vocabulary, _ := Find(defaultID)
	for _, appearance := range slices.Sorted(maps.Keys(override)) {
		if appearance != "dark" && appearance != "light" {
			return fmt.Errorf("%s is not an appearance; use dark or light", appearance)
		}
		groups, ok := override[appearance].(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object of ui and terminal colors", appearance)
		}
		for _, group := range slices.Sorted(maps.Keys(groups)) {
			known := vocabulary.Dark.group(group)
			if known == nil {
				return fmt.Errorf("%s.%s is not a group; use ui or terminal", appearance, group)
			}
			names, ok := groups[group].(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s is not an object of color names and colors", appearance, group)
			}
			for _, name := range slices.Sorted(maps.Keys(names)) {
				if _, ok := known[name]; !ok {
					return fmt.Errorf("%s.%s.%s is not a color name; %s names: %s", appearance, group, name, group, strings.Join(slices.Sorted(maps.Keys(known)), ", "))
				}
				if color, ok := names[name].(string); !ok || !colorFormats[group].pattern.MatchString(color) {
					shown, _ := json.Marshal(names[name])
					return fmt.Errorf("%s.%s.%s: %s is not a %s color; use %s", appearance, group, name, shown, group, colorFormats[group].spelled)
				}
			}
		}
	}
	return nil
}

func (a Appearance) group(name string) map[string]string {
	switch name {
	case "ui":
		return a.UI
	case "terminal":
		return a.Terminal
	}
	return nil
}
