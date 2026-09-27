package notebook

import "testing"

func TestCleanPath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"knowledge/areas/foo.md", "knowledge/areas/foo.md", false},
		{"/knowledge/areas/foo.md", "knowledge/areas/foo.md", false},
		{"  /index.md  ", "index.md", false},
		{"knowledge/./foo.md", "knowledge/foo.md", false},
		{"../../etc/passwd.md", "etc/passwd.md", false},
		{"knowledge/../journal/x.md", "journal/x.md", false},
		{"", "", true},
		{"/", "", true},
		{"knowledge/foo.txt", "", true},
		{"knowledge/foo", "", true},
		{".attn/raw/x.md", "", true},
		{"knowledge/.hidden.md", "", true},
		{"knowledge//foo.md", "knowledge/foo.md", false},
	}
	for _, tc := range tests {
		got, err := CleanPath(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("CleanPath(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("CleanPath(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("CleanPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
