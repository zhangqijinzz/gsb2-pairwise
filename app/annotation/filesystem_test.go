package annotation

import (
	"context"
	"strings"
	"testing"
)

func TestRunCommandIncludesStdoutWhenCommandFailsWithoutStderr(t *testing.T) {
	_, err := runCommand(context.Background(), t.TempDir(), "sh", "-c", "printf 'export validation detail'; exit 1")
	if err == nil || !strings.Contains(err.Error(), "export validation detail") {
		t.Fatalf("error = %v, want stdout diagnostic", err)
	}
}
