package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintInstanceBanner_NoopForDefault(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	var buf bytes.Buffer
	PrintInstanceBanner(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output for default instance, got %q", buf.String())
	}
}

func TestPrintInstanceBanner_MentionsInstanceSocketAndPort(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "dev")
	t.Setenv("ATTN_WS_PORT", "")
	var buf bytes.Buffer
	PrintInstanceBanner(&buf)
	got := buf.String()
	if !strings.Contains(got, "instance=dev") {
		t.Errorf("banner missing instance= field: %q", got)
	}
	if !strings.Contains(got, "socket=") {
		t.Errorf("banner missing socket= field: %q", got)
	}
	if !strings.Contains(got, "port=29849") {
		t.Errorf("banner missing port=29849: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("banner should end with newline, got %q", got)
	}
}

func TestCollapseHome(t *testing.T) {
	home := "/Users/victor"
	cases := map[string]string{
		"/Users/victor":                     "~",
		"/Users/victor/.attn-dev":           "~/.attn-dev",
		"/Users/victor/.attn-dev/attn.sock": "~/.attn-dev/attn.sock",
		"/Users/victor/":                    "~",
		"/tmp/other":                        "/tmp/other",
	}
	for in, want := range cases {
		if got := collapseHomeRelativeTo(in, home); got != want {
			t.Errorf("collapseHomeRelativeTo(%q, %q) = %q, want %q", in, home, got, want)
		}
	}
}
