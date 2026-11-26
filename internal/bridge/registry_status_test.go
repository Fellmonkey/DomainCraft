package bridge

import (
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
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	// Simulate an upstream with a newer commit but the same contract v1.
	stubGitHubAPI(t, "abcdef1234567890", "name: b\ncontract_version: 1\n")

	entry := *Default().ByID("csharp-core")
	if ok, err := TryMaterializeEmbedded(entry, nil); err != nil || !ok {
		t.Fatalf("materialize embedded: %v, %v", ok, err)
	}
	// An embedded copy has no git history; without a HEAD the remote probe
	// cannot tell a stale commit from the real one, so give the fixture one.
	makeBridgeRepo(t, CachePath(entry), "main")

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
