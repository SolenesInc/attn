package pty

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestKittyStorageLimitFromEnvironment(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  uint64
	}{
		{name: "unset is the shipping default", want: kittyStorageLimitDefault},
		{name: "empty is the shipping default", value: "", set: true, want: kittyStorageLimitDefault},
		{name: "an explicit zero disables the protocol", value: "0", set: true, want: 0},
		{name: "a byte count is the limit", value: "16777216", set: true, want: 16777216},
		{name: "surrounding space is tolerated", value: "  4096  ", set: true, want: 4096},
		{name: "a size suffix falls back to the default", value: "64MB", set: true, want: kittyStorageLimitDefault},
		{name: "a negative value falls back to the default", value: "-1", set: true, want: kittyStorageLimitDefault},
		{name: "words fall back to the default", value: "yes", set: true, want: kittyStorageLimitDefault},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(kittyStorageLimitEnv, "")
			if tc.set {
				t.Setenv(kittyStorageLimitEnv, tc.value)
			}
			var logs []string
			got := kittyStorageLimit(func(format string, args ...interface{}) {
				logs = append(logs, fmt.Sprintf(format, args...))
			})
			if got != tc.want {
				t.Errorf("kittyStorageLimit() = %d, want %d", got, tc.want)
			}
			_, parseErr := strconv.ParseUint(strings.TrimSpace(tc.value), 10, 64)
			wantLog := tc.set && tc.value != "" && parseErr != nil
			switch {
			case wantLog && len(logs) == 0:
				t.Errorf("%s=%q was ignored without a word about it", kittyStorageLimitEnv, tc.value)
			case !wantLog && len(logs) != 0:
				t.Errorf("%s=%q logged %q, want silence", kittyStorageLimitEnv, tc.value, logs)
			case wantLog:
				for _, want := range []string{kittyStorageLimitEnv, tc.value, fmt.Sprint(uint64(kittyStorageLimitDefault))} {
					if !strings.Contains(logs[0], want) {
						t.Errorf("log %q does not name %q", logs[0], want)
					}
				}
			}
		})
	}
}
