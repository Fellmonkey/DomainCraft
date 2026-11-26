package main

import (
	"os"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/netutil"
)

// updateOps is the full set of external effects used by the core self-update
// flow. Production builds use defaultUpdateOps(); tests construct a value with
// hermetic fakes and pass it explicitly — no package-level variable is swapped.
type updateOps struct {
	getBody         func(string) ([]byte, error)
	executable      func() (string, error)
	replaceExecutable func(tmp, exe string) error
	chmod           func(string, os.FileMode) error
	rename          func(src, dst string) error
	remove          func(string) error
	createTemp      func(dir, pattern string) (*os.File, error)
	maxBodyBytes    int64
	latestForNotice func() (*githubRelease, error)
}

func defaultUpdateOps() updateOps {
	o := updateOps{
		getBody:      httpGetBody,
		executable:   os.Executable,
		chmod:        os.Chmod,
		rename:       os.Rename,
		remove:       os.Remove,
		createTemp:   os.CreateTemp,
		maxBodyBytes: 64 << 20,
		latestForNotice: func() (*githubRelease, error) {
			body, err := netutil.BoundedGet(bridge.CommandContext(), noticeClient, releasesLatestURL, 64<<20, validateReleaseRedirect)
			if err != nil {
				return nil, err
			}
			return decodeRelease(body)
		},
	}
	o.replaceExecutable = func(tmp, exe string) error { return replaceExecutableWithOps(o, tmp, exe) }
	return o
}
