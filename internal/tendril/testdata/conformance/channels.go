package main

import (
	"fmt"
	"strconv"
	"strings"
)

func producer(n int, out chan<- int) {
	for i := range n {
		out <- i * i
	}
	close(out)
}

func worker(id int, jobs <-chan int, results chan<- string) {
	for j := range jobs {
		results <- fmt.Sprintf("w%d:%d", id, j*10)
	}
}

func fanIn(chs ...<-chan string) <-chan string {
	out := make(chan string)
	done := make(chan bool)
	for _, c := range chs {
		go func() {
			for v := range c {
				out <- v
			}
			done <- true
		}()
	}
	go func() {
		for range chs {
			<-done
		}
		close(out)
	}()
	return out
}

func gen(prefix string, n int) <-chan string {
	c := make(chan string)
	go func() {
		defer close(c)
		for i := range n {
			c <- fmt.Sprintf("%s%d", prefix, i)
		}
	}()
	return c
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	squares := make(chan int)
	go producer(5, squares)
	total := 0
	for v := range squares {
		total += v
	}
	add("producer: %d", total)

	buf := make(chan string, 3)
	buf <- "a"
	buf <- "b"
	add("buffered: %d %d", len(buf), cap(buf))
	first := <-buf
	add("first: %s", first)
	close(buf)
	second, ok := <-buf
	third, ok2 := <-buf
	add("drain: %q %v %q %v", second, ok, third, ok2)

	jobs := make(chan int, 10)
	results := make(chan string, 10)
	for w := 1; w <= 3; w++ {
		go worker(w, jobs, results)
	}
	for j := 1; j <= 6; j++ {
		jobs <- j
	}
	close(jobs)
	sum := 0
	for range 6 {
		r := <-results
		_, num, _ := strings.Cut(r, ":")
		v, _ := strconv.Atoi(num)
		sum += v
	}
	add("worker pool sum: %d", sum)

	var merged []string
	for v := range fanIn(gen("x", 3), gen("y", 2)) {
		merged = append(merged, v)
	}
	add("fan-in count: %d", len(merged))

	timeout := make(chan bool)
	data := make(chan int)
	go func() {
		data <- 42
	}()
	select {
	case v := <-data:
		add("select got %d", v)
	case <-timeout:
		add("timeout")
	}

	empty := make(chan int)
	select {
	case v := <-empty:
		add("unexpected %d", v)
	default:
		add("select default")
	}

	ping, pong := make(chan int), make(chan int)
	go func() {
		for v := range ping {
			pong <- v + 1
		}
		close(pong)
	}()
	v := 0
	for range 5 {
		ping <- v
		v = <-pong
	}
	close(ping)
	_, open := <-pong
	add("ping-pong: %d %v", v, open)

	quit := make(chan struct{})
	ticks := make(chan int)
	go func() {
		defer close(ticks)
		for i := 0; ; i++ {
			select {
			case ticks <- i:
			case <-quit:
				return
			}
		}
	}()
	got := 0
	for t := range ticks {
		got += t
		if t == 4 {
			close(quit)
			break
		}
	}
	add("quit channel: %d", got)

	var nilCh chan int
	select {
	case nilCh <- 1:
		add("unreachable")
	default:
		add("nil channel never ready")
	}

	sem := make(chan struct{}, 2)
	done := make(chan int)
	for i := range 4 {
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			done <- i
		}()
	}
	seen := 0
	for range 4 {
		seen += <-done
	}
	add("semaphore: %d", seen)
	return strings.Join(out, "\n")
}
