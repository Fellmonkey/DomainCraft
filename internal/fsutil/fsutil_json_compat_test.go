package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJSONV1Compatibility pins the decoding behavior of durable files
// (snapshot, cache, bridge metadata). encoding/json/v2 rejects duplicate keys
// and invalid UTF-8, so switching to it requires an explicit compatibility
// decision with fixtures — this test documents the v1 contract in the meantime.
func TestJSONV1Compatibility(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "duplicate keys keep last value", data: `{"v":"old","v":"new"}`, want: "new"},
		{name: "invalid utf8 is replaced", data: "{\"v\":\"\xff\"}", want: "\uFFFD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}
			var got struct {
				V string `json:"v"`
			}
			if err := ReadJSON(path, &got); err != nil {
				t.Fatal(err)
			}
			if got.V != tt.want {
				t.Fatalf("decoded %q, want %q", got.V, tt.want)
			}
		})
	}
}
