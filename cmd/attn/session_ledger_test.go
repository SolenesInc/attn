package main

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestSessionListPresetsStartOnLocalCalendarDays(t *testing.T) {
	zones := []string{"UTC", "America/New_York", "America/Sao_Paulo", "America/Havana", "Europe/Berlin", "Asia/Beirut", "Asia/Kolkata", "Australia/Lord_Howe", "Pacific/Chatham"}
	daysBack := map[string]int{"today": 0, "yesterday": 1, "7d": 6, "30d": 29}
	presets := []string{"today", "yesterday", "7d", "30d"}
	rapid.Check(t, func(t *rapid.T) {
		loc, err := time.LoadLocation(rapid.SampledFrom(zones).Draw(t, "zone"))
		if err != nil {
			t.Skip(err)
		}
		drawn := time.Unix(rapid.Int64Range(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC).Unix()).Draw(t, "instant"), 0)
		transition, next := drawn.In(loc).ZoneBounds()
		if !next.IsZero() {
			transition = next
		}
		now := transition.Add(time.Duration(rapid.Int64Range(0, 8*24*3600).Draw(t, "after transition")) * time.Second).In(loc)
		preset := rapid.SampledFrom(presets).Draw(t, "preset")

		since, until, err := sessionListPresetWindow(preset, now)
		if err != nil {
			t.Fatalf("%s: %v", preset, err)
		}
		startsDay := func(stamp string, back int) {
			at, err := time.Parse(time.RFC3339, stamp)
			if err != nil {
				t.Fatalf("%s window bound %q is not RFC3339: %v", preset, stamp, err)
			}
			day := time.Date(now.Year(), now.Month(), now.Day()-back, 12, 0, 0, 0, time.UTC)
			before := day.AddDate(0, 0, -1)
			local, previous := at.In(loc), at.Add(-time.Second).In(loc)
			if local.Year() != day.Year() || local.YearDay() != day.YearDay() || previous.Year() != before.Year() || previous.YearDay() != before.YearDay() {
				t.Fatalf("%s at %s: bound %s is not the start of %s in %s", preset, now, stamp, day.Format("2006-01-02"), loc)
			}
		}
		startsDay(since, daysBack[preset])
		if preset == "yesterday" {
			startsDay(until, 0)
		} else if until != "" {
			t.Fatalf("%s at %s has an end %s; only yesterday is closed", preset, now, until)
		}
	})
}
