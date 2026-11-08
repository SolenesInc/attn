package main

import (
	"testing"
	"time"
)

func TestSessionSnoozeCalendarAndDurationMeanings(t *testing.T) {
	location, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ now, when, want string }{
		{"2026-10-24T14:20:00+02:00", "30m", "2026-10-24T14:50:00+02:00"},
		{"2026-10-24T14:20:00+02:00", "1h", "2026-10-24T15:20:00+02:00"},
		{"2026-10-24T14:20:00+02:00", "2h", "2026-10-24T16:20:00+02:00"},
		{"2026-10-24T14:20:00+02:00", "4h", "2026-10-24T18:20:00+02:00"},
		{"2026-10-24T14:20:00+02:00", "8h", "2026-10-24T22:20:00+02:00"},
		{"2026-10-24T14:20:00+02:00", "tomorrow", "2026-10-25T09:00:00+01:00"},
		{"2026-03-28T14:20:00+01:00", "tomorrow", "2026-03-29T09:00:00+02:00"},
		{"2026-12-31T14:20:00+01:00", "tomorrow 11:30", "2027-01-01T11:30:00+01:00"},
		{"2026-10-24T08:00:00+02:00", "saturday", "2026-10-24T09:00:00+02:00"},
		{"2026-10-24T09:00:00+02:00", "saturday", "2026-10-31T09:00:00+01:00"},
		{"2026-10-26T08:00:00+01:00", "monday", "2026-10-26T09:00:00+01:00"},
		{"2026-10-26T14:00:00+01:00", "monday", "2026-11-02T09:00:00+01:00"},
		{"2026-10-24T14:20:00+02:00", "2026-10-25T10:30:00.123Z", "2026-10-25T10:30:00.123Z"},
	} {
		t.Run(tc.when+tc.now, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.now)
			got, err := sessionSnoozeInstant(tc.when, now.In(location))
			want, _ := time.Parse(time.RFC3339Nano, tc.want)
			if err != nil || !got.Equal(want) {
				t.Errorf("%q at %s = %s, %v; want %s", tc.when, tc.now, got, err, want)
			}
		})
	}
	for _, when := range []string{"", "0h", "-1h", "soon", "tomorrow 25:00", "tomorrow 09:00 extra", "2020-01-01T00:00:00Z"} {
		if _, err := sessionSnoozeInstant(when, time.Now()); err == nil {
			t.Errorf("accepted %q", when)
		}
	}
}
