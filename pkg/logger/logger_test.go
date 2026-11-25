package logger

import (
	"os"
	"testing"
)

func TestNewWritesDiagnosticsToStderr(t *testing.T) {
	if got := New().writer; got != os.Stderr {
		t.Errorf("default writer = %v, want os.Stderr", got)
	}
}
