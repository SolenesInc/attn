package colors

import (
	"maps"
	"slices"
	"testing"
)

func TestEverySchemeDefinesExactlyAttnNames(t *testing.T) {
	attn, ok := Find(defaultID)
	if !ok {
		t.Fatal("attn.json is not embedded")
	}
	for _, scheme := range Schemes() {
		for _, appearance := range []struct {
			name   string
			colors Appearance
		}{{"dark", scheme.Dark}, {"light", scheme.Light}} {
			for _, group := range []string{"ui", "terminal"} {
				want := slices.Sorted(maps.Keys(attn.Dark.group(group)))
				got := appearance.colors.group(group)
				if names := slices.Sorted(maps.Keys(got)); !slices.Equal(names, want) {
					t.Errorf("%s %s.%s names %v, want %v", scheme.ID, appearance.name, group, names, want)
				}
				for name, color := range got {
					if !colorFormats[group].pattern.MatchString(color) {
						t.Errorf("%s %s.%s.%s is %q, not %s", scheme.ID, appearance.name, group, name, color, colorFormats[group].spelled)
					}
				}
			}
		}
	}
	if Schemes()[0].ID != defaultID {
		t.Errorf("first scheme is %s, want attn", Schemes()[0].ID)
	}
}
