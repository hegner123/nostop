package tui

import (
	"fmt"
	"runtime"
)

// assert panics if the condition is false. Use for programmer invariants.
func assert(condition bool, msg string) {
	if !condition {
		_, file, line, ok := runtime.Caller(1)
		if ok {
			panic(fmt.Sprintf("assertion failed at %s:%d: %s", file, line, msg))
		}
		panic("assertion failed: " + msg)
	}
}

// assertf panics with a formatted message if the condition is false.
func assertf(condition bool, format string, args ...any) {
	if !condition {
		_, file, line, ok := runtime.Caller(1)
		msg := fmt.Sprintf(format, args...)
		if ok {
			panic(fmt.Sprintf("assertion failed at %s:%d: %s", file, line, msg))
		}
		panic("assertion failed: " + msg)
	}
}

// unreachable panics unconditionally for impossible branches.
func unreachable(format string, args ...any) {
	_, file, line, ok := runtime.Caller(1)
	msg := fmt.Sprintf(format, args...)
	if ok {
		panic(fmt.Sprintf("unreachable at %s:%d: %s", file, line, msg))
	}
	panic("unreachable: " + msg)
}
