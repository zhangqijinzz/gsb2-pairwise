package annotation

import (
	"strings"
	"testing"
)

func TestSliceTraceRoundKeepsOnlyRequestedLineRange(t *testing.T) {
	trace := []byte("one\ntwo\nthree\nfour\n")
	got, err := SliceTraceRound(trace, Round{SourceStart: 2, SourceEnd: 3})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "two\nthree\n" {
		t.Fatalf("SliceTraceRound() = %q", got)
	}
}

func TestSliceTraceRoundRejectsInvalidBounds(t *testing.T) {
	if _, err := SliceTraceRound([]byte("one\n"), Round{SourceStart: 0, SourceEnd: 1}); err == nil || !strings.Contains(err.Error(), "range") {
		t.Fatalf("SliceTraceRound() error = %v", err)
	}
}
