package annotation

import (
	"bytes"
	"fmt"
)

// SliceTraceRound returns the exact source lines recorded for one real round.
// The complete trace remains in the frozen capture for later audit.
func SliceTraceRound(trace []byte, round Round) ([]byte, error) {
	if round.SourceStart < 1 || round.SourceEnd < round.SourceStart {
		return nil, fmt.Errorf("invalid round source range %d-%d", round.SourceStart, round.SourceEnd)
	}
	lines := bytes.Split(trace, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if round.SourceEnd > len(lines) {
		return nil, fmt.Errorf("round source range %d-%d exceeds %d trace lines", round.SourceStart, round.SourceEnd, len(lines))
	}
	selected := bytes.Join(lines[round.SourceStart-1:round.SourceEnd], []byte{'\n'})
	return append(selected, '\n'), nil
}
