package logger

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestNewWritesDiagnosticsToStderr(t *testing.T) {
	if got := New().writer; got != os.Stderr {
		t.Errorf("default writer = %v, want os.Stderr", got)
	}
}

func TestLoggerOmitsANSIWhenNotTTY(t *testing.T) {
	// No t.Parallel: New() probes the process stderr and NO_COLOR, both global.
	var buf bytes.Buffer
	log := New()
	log.SetWriter(&buf)
	log.SetColors(false)

	log.Info("info %d", 1)
	log.Success("success %d", 2)
	log.Warn("warn %d", 3)
	log.Error("error %d", 4)

	out := buf.String()
	if bytes.Contains(buf.Bytes(), []byte{0x1B}) {
		t.Errorf("output = %q, want no ANSI escape bytes", out)
	}
	for _, want := range []string{"info 1", "success 2", "warn 3", "error 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}

	buf.Reset()
	log.SetColors(true)
	log.Info("colored")
	if !bytes.Contains(buf.Bytes(), []byte{0x1B}) {
		t.Errorf("output = %q, want an ANSI escape after SetColors(true)", buf.String())
	}
}
