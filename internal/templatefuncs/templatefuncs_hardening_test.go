package templatefuncs

import "testing"

func TestSetInitializesNilMap(t *testing.T) {
	t.Parallel()
	got := Set(nil, "key", "value")
	if got == nil || got["key"] != "value" {
		t.Fatalf("Set(nil, ...) = %v, want map with key=value", got)
	}
}
