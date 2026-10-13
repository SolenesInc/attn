package main

import (
	"fmt"
	"strings"
)

var calls int

var table = buildTable()

func buildTable() map[string]int {
	calls++
	return map[string]int{"one": 1, "two": 2}
}

func classify(n int) string {
	switch {
	case n < 0:
		return "negative"
	case n == 0:
		return "zero"
	case n%2 == 0:
		return "even"
	}
	return "odd"
}

func grade(score int) string {
	switch score / 10 {
	case 10, 9:
		return "A"
	case 8:
		return "B"
	default:
		return "F"
	}
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	add("globals: %d %d", table["two"], calls)
	for _, n := range []int{-3, 0, 4, 7} {
		add("%d is %s", n, classify(n))
	}
	add("grades: %s %s %s", grade(95), grade(80), grade(12))

	total := 0
	for i := 0; i < 100; i++ {
		if i%3 == 0 {
			continue
		}
		if i > 20 {
			break
		}
		switch {
		case i == 5:
			total += 1000
			break
		}
		total += i
	}
	add("loop: %d", total)

	count := 0
	for count < 5 {
		count += 2
	}
	add("while: %d", count)
	k := 0
	for {
		k++
		if k == 7 {
			break
		}
	}
	add("forever: %d", k)
	sq := 0
	for i := range 5 {
		sq += i * i
	}
	add("range int: %d", sq)

	primes := []int{}
	for n := 2; len(primes) < 10; n++ {
		isPrime := true
		for _, p := range primes {
			if p*p > n {
				break
			}
			if n%p == 0 {
				isPrime = false
				break
			}
		}
		if isPrime {
			primes = append(primes, n)
		}
	}
	add("primes: %v", primes)
	if v, ok := table["one"]; ok && v == 1 {
		add("if-init: %d", v)
	} else if v == 0 {
		add("unreachable")
	}
	return strings.Join(out, "\n")
}
