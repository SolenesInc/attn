package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

type SafeCounter struct {
	mu sync.Mutex
	m  map[string]int
}

func (c *SafeCounter) Inc(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key]++
}

type Cache struct {
	mu   sync.RWMutex
	data map[string]string
}

func (c *Cache) Get(k string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.data[k]
	return v, ok
}

func (c *Cache) Put(k, v string) {
	c.mu.Lock()
	c.data[k] = v
	c.mu.Unlock()
}

func fetch(id int) (string, error) {
	if id == 3 {
		return "", errors.New("item 3 unavailable")
	}
	return fmt.Sprintf("item-%d", id), nil
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	var wg sync.WaitGroup
	results := make([]int, 10)
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = i * i
		}()
	}
	wg.Wait()
	add("waitgroup: %v", results)

	c := SafeCounter{m: map[string]int{}}
	var wg2 sync.WaitGroup
	for i := range 100 {
		wg2.Go(func() {
			c.Inc([]string{"a", "b"}[i%2])
		})
	}
	wg2.Wait()
	add("mutex counter: %v", c.m)

	cache := &Cache{data: map[string]string{}}
	var writers sync.WaitGroup
	for i := range 5 {
		writers.Go(func() { cache.Put(fmt.Sprint(i), strings.Repeat("x", i)) })
	}
	writers.Wait()
	v, ok := cache.Get("3")
	add("rwmutex: %q %v %d", v, ok, len(cache.data))

	var once sync.Once
	inits := 0
	var wg3 sync.WaitGroup
	for range 5 {
		wg3.Go(func() { once.Do(func() { inits++ }) })
	}
	wg3.Wait()
	add("once: %d", inits)

	var g errgroup.Group
	fetched := make([]string, 5)
	for i := range 5 {
		g.Go(func() error {
			s, err := fetch(i)
			if err != nil {
				return fmt.Errorf("fetch %d: %w", i, err)
			}
			fetched[i] = s
			return nil
		})
	}
	err := g.Wait()
	add("errgroup: %v %q", err, fetched)

	var limited errgroup.Group
	limited.SetLimit(2)
	var mu sync.Mutex
	active, peak := 0, 0
	var order []int
	for i := range 6 {
		limited.Go(func() error {
			mu.Lock()
			active++
			peak = max(peak, active)
			order = append(order, i)
			mu.Unlock()
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		})
	}
	add("limit: %v peak<=2:%v", limited.Wait(), peak <= 2)
	slices.Sort(order)
	add("all ran: %v", order)

	var tl sync.Mutex
	add("trylock: %v %v", tl.TryLock(), tl.TryLock())
	tl.Unlock()
	return strings.Join(out, "\n")
}
