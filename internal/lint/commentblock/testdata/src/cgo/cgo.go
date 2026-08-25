package cgo

/*
#include <stdint.h>
#include <stddef.h>
static int one(void) { return 1; }
*/
import "C"

func One() int { return int(C.one()) }
