package ingest

import (
	"sync"
	"time"

	"github.com/victorarias/attn/internal/analytics"
)

type window struct {
	limit  string
	value  int64
	span   time.Duration
	bucket time.Duration
}

type quota struct {
	windows []window
	mu      sync.Mutex
	counts  map[string][]*slidingCount
	swept   time.Time
}

func newQuota(windows ...window) *quota {
	return &quota{windows: windows, counts: map[string][]*slidingCount{}}
}

func (q *quota) admit(key string, n int64, now time.Time) *analytics.Refusal {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweep(now)
	counts := q.countsFor(key)
	for i, w := range q.windows {
		counts[i].advance(w, now)
		if observed := counts[i].total() + n; observed > w.value {
			return &analytics.Refusal{
				Limit:       w.limit,
				Value:       w.value,
				Observed:    observed,
				RetryAfterS: counts[i].secondsUntilFree(w, observed-w.value, now),
			}
		}
	}
	for i, w := range q.windows {
		counts[i].add(w, n)
	}
	return nil
}

func (q *quota) countsFor(key string) []*slidingCount {
	counts, ok := q.counts[key]
	if !ok {
		counts = make([]*slidingCount, len(q.windows))
		for i, w := range q.windows {
			counts[i] = &slidingCount{slots: make([]int64, w.span/w.bucket)}
		}
		q.counts[key] = counts
	}
	return counts
}

func (q *quota) sweep(now time.Time) {
	var longest time.Duration
	for _, w := range q.windows {
		longest = max(longest, w.span)
	}
	if now.Sub(q.swept) < longest {
		return
	}
	q.swept = now
	for key, counts := range q.counts {
		empty := true
		for i, w := range q.windows {
			counts[i].advance(w, now)
			empty = empty && counts[i].total() == 0
		}
		if empty {
			delete(q.counts, key)
		}
	}
}

type slidingCount struct {
	slots  []int64
	newest int64
}

func (c *slidingCount) advance(w window, now time.Time) {
	current := now.UnixNano() / int64(w.bucket)
	size := int64(len(c.slots))
	for step := c.newest + 1; step <= current && step <= c.newest+size; step++ {
		c.slots[step%size] = 0
	}
	if current > c.newest {
		c.newest = current
	}
}

func (c *slidingCount) total() int64 {
	var sum int64
	for _, n := range c.slots {
		sum += n
	}
	return sum
}

func (c *slidingCount) add(w window, n int64) {
	c.slots[c.newest%int64(len(c.slots))] += n
}

func (c *slidingCount) secondsUntilFree(w window, excess int64, now time.Time) int64 {
	size := int64(len(c.slots))
	var freed int64
	for step := c.newest - size + 1; step <= c.newest; step++ {
		freed += c.slots[step%size]
		if freed >= excess {
			expires := time.Unix(0, (step+size)*int64(w.bucket))
			return max(1, int64((expires.Sub(now)+time.Second-1)/time.Second))
		}
	}
	return int64(w.span / time.Second)
}
