package main

import "testing"

func TestResolveVersion(t *testing.T) {
	cases := []struct {
		name, stamped, buildInfo, want string
	}{
		{"stamped wins", "v0.5.0", "v0.5.0", "v0.5.0"},
		{"go install clean tag", "dev", "v0.5.0", "v0.5.0"},
		{"local build", "dev", "(devel)", "dev"},
		{"pseudo-version", "dev", "v0.5.1-0.20260821125410-876219469a43", "dev"},
		{"pseudo-version with metadata", "dev", "v0.5.1-0.20260821125410-876219469a43+dirty", "dev"},
		{"no build info", "dev", "", "dev"},
		{"empty stamp clean tag", "", "v0.5.0", "v0.5.0"},
		{"empty stamp devel", "", "(devel)", "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveVersion(c.stamped, c.buildInfo); got != c.want {
				t.Errorf("resolveVersion(%q, %q) = %q, want %q", c.stamped, c.buildInfo, got, c.want)
			}
		})
	}
}
