package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSatisfactionActivityPreservesSplitJSONLines(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	w := &satisfactionLogWriter{file: f, onLine: func(s string) { lines = append(lines, s) }}
	for _, part := range []string{"{\"type\":\"item.", "started\"}\n{\"type\":", "\"turn.completed\"}\n"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{`{"type":"item.started"}`, `{"type":"turn.completed"}`}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("fragmented activity callbacks: %q", lines)
	}
}
