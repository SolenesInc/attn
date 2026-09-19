package a // want "comment found"

import "fmt"

// One line. // want "comment found"
func one() {}

/* A block comment over lines. */ // want "comment found"
func block()                      {}

var trailing = 1 // trailing // want "comment found"

//go:generate echo directives pass
func directive() {}

//nolint:all
func nolint() {}

// prose above a directive // want "comment found"
//
//go:noinline
func mixed() {}

func inner() {
	// indented // want "comment found"
	_ = 0
}

func ExampleOne() {
	fmt.Println("x")
	// Output:
	// x
}

func ExampleTwo() {
	fmt.Println("x")
	// unordered output:
	// x
}

func outputImpostor() {
	// Output: // want "comment found"
}
