// Package a is a fixture.
package a

// One line.
func one() {}

// Two lines are the
// limit and pass.
func two() {}

// want "comment block spans 3 lines"
// Three lines
// fail.
func three() {}

// want "comment block spans 4 lines"
/* A block comment
   over three
   lines fails. */
func block() {}

/* Two-line block
passes. */

func blockTwo() {}

var trailing = 1 // trailing comment
// then a full-line comment
// makes a separate two-line block, which passes

// receipt line one
//
//go:generate echo directives exempt the whole group
func directive() {}

// a
// b
//
//nolint:all
func nolint() {}

func inner() {
	// want "comment block spans 3 lines"
	// indented
	// three
	_ = 0
}

// separated by a blank line

// from this one, so each is its own block
func gap() {}
