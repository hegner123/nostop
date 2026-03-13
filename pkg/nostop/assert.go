// Package nostop provides the main Nostop engine for intelligent topic-based context archival.
package nostop

import (
	"fmt"
	"runtime"
)

// assert panics if the condition is false. Use for programmer invariants —
// conditions that indicate a bug in the code, not a runtime failure.
//
// Distinguish from errors: errors are expected (network down, bad input).
// Assertions are for things that should never happen if the code is correct.
//
// Examples:
//
//	assert(cfg.APIKey != "", "Config.APIKey must be set before calling New")
//	assert(len(topics) <= maxTopics, "topic count exceeds capacity")
//	assert(score >= 0 && score <= 1.0, "relevance score out of range [0,1]")
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

// unreachable panics unconditionally. Use in default branches of exhaustive
// switches where all valid cases are handled.
//
// Example:
//
//	switch zone {
//	case ZoneNormal:  ...
//	case ZoneMonitor: ...
//	case ZoneWarning: ...
//	case ZoneArchive: ...
//	default:
//	    unreachable("invalid ContextZone %d", zone)
//	}
func unreachable(format string, args ...any) {
	_, file, line, ok := runtime.Caller(1)
	msg := fmt.Sprintf(format, args...)
	if ok {
		panic(fmt.Sprintf("unreachable at %s:%d: %s", file, line, msg))
	}
	panic("unreachable: " + msg)
}
