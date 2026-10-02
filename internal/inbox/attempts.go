package inbox

import "time"

const (
	AttemptDelay = 5 * time.Minute // F1: healthy agents read p99.5 60s after a ring.
	MaxAttempts  = 3               // F2: one ring suffices for 99%+; repeats were outages.
)

type Receipt struct {
	ItemID string
	Rang   bool
	Detail string
}

// Due returns the next attempt time; any read since the last attempt releases its outstanding ring.
func Due(last, read, now time.Time) time.Time {
	if last.IsZero() || !read.Before(last) || !now.Before(last.Add(AttemptDelay)) {
		return now
	}
	return last.Add(AttemptDelay)
}
