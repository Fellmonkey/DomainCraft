package bridge

import (
	"os"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// TestStatusLocalFacts covers embedded/cached without any network access.
func TestStatusLocalFacts(t *testing.T) {
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	entry := *Default().ByID("csharp-core")
	st := entry.Status(false)
	if !st.Embedded {
		t.Error("csharp-core should be embedded (in the binary)")
	}
	if st.Cached {
		t.Error("csharp-core should not be cached yet (fresh test cache)")
	}
	if st.Outdated || st.RemoteReachable {
		t.Error("Status(false) must not contact the remote")
	}

	// Materialize the embedded copy → now cached and embedded.
	if ok, err := TryMaterializeEmbedded(entry, nil); err != nil || !ok {
		t.Fatalf("materialize embedded: %v, %v", ok, err)
	}
	st = entry.Status(false)
	if !st.Cached {
		t.Error("csharp-core should be cached after materialization")
	}
}

// TestStatusRemoteOnlyWhenRequested ensures CheckRemote=true contacts the remote
// and reports outdated only for a supported (not newer-contract) bridge.
func TestStatusRemoteOnlyWhenRequested(t *testing.T) {
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	orig := httpGetBodyFn
	defer func() { httpGetBodyFn = orig }()

	httpGetBodyFn = func(url string) ([]byte, error) {
		if !containsAny(url, "/repos/", "/commits/", "/bridge.yaml") {
			return nil, os.ErrNotExist
		}
		// Simulate an upstream with a newer commit but the same contract v1.
		if containsAny(url, "/commits/") {
			return []byte(`{"sha":"abcdef1234567890"}`), nil
		}
		if containsAny(url, "/bridge.yaml") {
			return []byte("name: b\ncontract_version: 1\n"), nil
		}
		if containsAny(url, "/repos/") {
			return []byte(`{"default_branch":"main"}`), nil
		}
		return nil, os.ErrNotExist
	}

	entry := *Default().ByID("csharp-core")
	if ok, err := TryMaterializeEmbedded(entry, nil); err != nil || !ok {
		t.Fatalf("materialize embedded: %v, %v", ok, err)
	}

	st := entry.Status(true)
	if !st.Embedded || !st.Cached {
		t.Errorf("expected embedded+cached, got %+v", st)
	}
	if !st.RemoteReachable {
		t.Errorf("remote should be reachable, got %+v", st)
	}
	if !st.Outdated {
		t.Errorf("a supported newer version should be flagged as outdated")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
