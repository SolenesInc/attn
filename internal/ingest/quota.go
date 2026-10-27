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

type charge struct {
	window window
	key    string
	n      int64
}

type countKey struct {
	limit string
	key   string
}

type quota struct {
	mu      sync.Mutex
	counts  map[countKey]*slidingCount
	longest time.Duration
	swept   time.Time
}

func newQuota() *quota {
	return &quota{counts: map[countKey]*slidingCount{}}
}

func (q *quota) admit(now time.Time, charges ...charge) *analytics.Refusal {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweep(now)
	counts := make([]*slidingCount, len(charges))
	for i, c := range charges {
		counts[i] = q.countFor(c)
		counts[i].advance(now)
		if observed := counts[i].total() + c.n; observed > c.window.value {
			return &analytics.Refusal{
				Limit:       c.window.limit,
				Value:       c.window.value,
				Observed:    observed,
				RetryAfterS: counts[i].secondsUntilFree(observed-c.window.value, now),
			}
		}
	}
	for i, c := range charges {
		counts[i].add(c.n)
	}
	return nil
}

func (q *quota) countFor(c charge) *slidingCount {
	key := countKey{limit: c.window.limit, key: c.key}
	count, ok := q.counts[key]
	if !ok {
		count = &slidingCount{window: c.window, slots: make([]int64, c.window.span/c.window.bucket)}
		q.counts[key] = count
		q.longest = max(q.longest, c.window.span)
	}
	return count
}

func (q *quota) sweep(now time.Time) {
	if now.Sub(q.swept) < q.longest {
		return
	}
	q.swept = now
	for key, count := range q.counts {
		count.advance(now)
		if count.total() == 0 {
			delete(q.counts, key)
		}
	}
}

type slidingCount struct {
	window window
	slots  []int64
	newest int64
}

func (c *slidingCount) advance(now time.Time) {
	current := now.UnixNano() / int64(c.window.bucket)
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

func (c *slidingCount) add(n int64) {
	c.slots[c.newest%int64(len(c.slots))] += n
}

func (c *slidingCount) secondsUntilFree(excess int64, now time.Time) int64 {
	size := int64(len(c.slots))
	var freed int64
	for step := c.newest - size + 1; step <= c.newest; step++ {
		freed += c.slots[step%size]
		if freed >= excess {
			expires := time.Unix(0, (step+size)*int64(c.window.bucket))
			return max(1, int64((expires.Sub(now)+time.Second-1)/time.Second))
		}
	}
	return int64(c.window.span / time.Second)
}
