package daemon

import (
	"strings"
	"testing"
)

func TestSanitizeSessionTitle(t *testing.T) {
	longMultibyte := strings.Repeat("é", 60)
	wantLongMultibyte := strings.Repeat("é", maxSessionNameRunes)

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "Fix login flow", "Fix login flow"},
		{"double_quotes", `"Fix login flow"`, "Fix login flow"},
		{"backticks", "`Fix login flow`", "Fix login flow"},
		{"curly_quotes", "“Fix login flow”", "Fix login flow"},
		{"title_prefix", "Title: Fix login flow", "Fix login flow"},
		{"title_prefix_lower", "title: fix login flow", "fix login flow"},
		{"multiline_first_nonempty", "\n  \nFix login flow\nsecond line", "Fix login flow"},
		{"internal_whitespace_collapsed", "Fix   login\tflow", "Fix login flow"},
		{"trailing_punctuation", "Fix login flow.", "Fix login flow"},
		{"trailing_punctuation_mixed", "Fix login flow!;", "Fix login flow"},
		{"over_limit_multibyte", longMultibyte, wantLongMultibyte},
		{"empty", "", ""},
		{"whitespace_only", "   \n\t  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeSessionTitle(tc.raw)
			if got != tc.want {
				t.Errorf("sanitizeSessionTitle(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
