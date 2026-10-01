package codexshared

import "testing"

func TestDisplayedRootTitleCorpus(t *testing.T) {
	a := "01a0f7e4-6f86-7581-bd7e-142da1111111"
	b := "01a0f7e4-6f86-7581-bd7e-142da2222222"
	for _, tc := range []struct {
		title string
		roots []string
		want  string
	}{
		{a, []string{a, b}, a},
		{a + " ⠋", []string{a, b}, a},
		{"01a0f7e4-6f86-7581-bd7e-142da...", []string{a}, a},
		{"01a0f7e4-6f86-7581-bd7e-142da... ⠋", []string{a, b}, ""},
		{"[ . ] Action Required | " + b, []string{a, b}, b},
		{"Codex", []string{a}, ""}, {"", []string{a}, ""},
		{"unregistered-cwd", []string{a}, ""}, {a, nil, ""},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if got := ResolveTitle(tc.title, tc.roots); got != tc.want {
				t.Fatalf("ResolveTitle(%q,%q)=%q, want %q", tc.title, tc.roots, got, tc.want)
			}
		})
	}
}
