package snapshot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
)

// safeRelPath resolves a snapshot file path against outputDir and rejects
// anything that escapes it. The snapshot lives in the output directory, is
// often shared or committed, and --prune applies its paths without prompting;
// a "path": "../../..." entry must never reach os.Remove, os.Rename or a write.
//
// It uses the same symlink policy as the renderer and the mutation helpers:
// every existing component, including outputDir itself, must be a real
// directory or file. A link that resolves back inside outputDir is refused too,
// so a snapshot path means the same thing for reads, writes and cleanup.
func safeRelPath(outputDir, relPath string) (string, error) {
	abs, _, err := fsutil.LocalPathWithin(outputDir, relPath)
	if err != nil {
		return "", fmt.Errorf("unsafe snapshot path %q: %w", relPath, err)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve output dir %q: %w", outputDir, err)
	}
	if err := fsutil.RefuseSymlinks(absOutput, abs); err != nil {
		return "", fmt.Errorf("unsafe snapshot path %q: %w", relPath, err)
	}
	return abs, nil
}

// openRootFor validates relPath against outputDir and returns a root confined
// to that directory plus the OS-form path relative to it. Mutating operations
// must use the returned root so a component replaced by a symlink after the
// lexical and symlink checks cannot redirect the write or delete.
func openRootFor(outputDir, relPath string) (*os.Root, string, error) {
	abs, err := safeRelPath(outputDir, relPath)
	if err != nil {
		return nil, "", err
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve output dir %q: %w", outputDir, err)
	}
	return fsutil.OpenRootFor(absOutput, abs)
}
