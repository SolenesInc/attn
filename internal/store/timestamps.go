package store

import "time"

func formatStoredTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
