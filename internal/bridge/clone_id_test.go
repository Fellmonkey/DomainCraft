package bridge

import "testing"

func TestValidateCacheIDRejectsEscapes(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "simple id", id: "csharp-restful"},
		{name: "underscore and digits", id: "my_bridge2"},
		{name: "empty", id: "", wantErr: true},
		{name: "dot", id: ".", wantErr: true},
		{name: "parent", id: "..", wantErr: true},
		{name: "slash", id: "bar/../../Documents", wantErr: true},
		{name: "backslash", id: `bar\..\..\Documents`, wantErr: true},
		{name: "absolute", id: `/etc/passwd`, wantErr: true},
		{name: "drive", id: `C:\Users\secret`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCacheID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateCacheID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}
