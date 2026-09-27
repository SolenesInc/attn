package pty

import (
	"testing"
)

func TestContainsCPRQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "contains cpr query",
			data: []byte("\x1b[6n"),
			want: true,
		},
		{
			name: "ignores other dsr query",
			data: []byte("\x1b[5n"),
			want: false,
		},
		{
			name: "ignores malformed sequence",
			data: []byte("\x1b[6x"),
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsCPRQuery(tc.data); got != tc.want {
				t.Fatalf("containsCPRQuery() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScanOSCColorQueriesContainsCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		code int
		want bool
	}{
		{
			name: "contains osc 10 query",
			data: []byte("\x1b]10;?\x1b\\"),
			code: 10,
			want: true,
		},
		{
			name: "contains osc 11 query",
			data: []byte("\x1b]11;?\x07"),
			code: 11,
			want: true,
		},
		{
			name: "ignores different osc query",
			data: []byte("\x1b]11;?\x1b\\"),
			code: 10,
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			codes := scanOSCColorQueries(tc.data)
			got := false
			for _, c := range codes {
				if c == tc.code {
					got = true
					break
				}
			}
			if got != tc.want {
				t.Fatalf("scanOSCColorQueries(%q) contains %d = %v, want %v", tc.data, tc.code, got, tc.want)
			}
		})
	}
}

func TestDetectTerminalQueries(t *testing.T) {
	t.Parallel()

	queries := detectTerminalQueries([]byte("\x1b[6n...\x1b[c...\x1b]10;?\x1b\\...\x1b]11;?\x07...\x1b]12;?\x07"))
	if !queries.da1 || !queries.cpr || queries.osc10 != 1 || queries.osc11 != 1 || queries.osc12 != 1 {
		t.Fatalf("detectTerminalQueries() = %+v, want all queries detected once", queries)
	}
	if queries.da1BeforeCPR {
		t.Fatalf("detectTerminalQueries() = %+v, want da1BeforeCPR=false for CPR-first chunk", queries)
	}

	reversed := detectTerminalQueries([]byte("\x1b[0c...\x1b[6n"))
	if !reversed.da1BeforeCPR {
		t.Fatalf("detectTerminalQueries() = %+v, want da1BeforeCPR=true for DA1-first chunk", reversed)
	}

	repeated := detectTerminalQueries([]byte("\x1b]11;?\x07\x1b]11;?\x07\x1b]11;?\x07"))
	if repeated.osc11 != 3 {
		t.Fatalf("detectTerminalQueries() osc11 = %d, want 3", repeated.osc11)
	}

	set := detectTerminalQueries([]byte("\x1b]11;#000000\x1b\\"))
	if set.osc11 != 0 {
		t.Fatalf("detectTerminalQueries() osc11 = %d, want 0 for a color SET", set.osc11)
	}
}
