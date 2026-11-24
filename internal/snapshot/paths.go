package snapshot

import (
	"fmt"
	"path/filepath"
	"strings"
)

// safeRelPath resolves a snapshot file path against outputDir and rejects
// anything that escapes it. The snapshot lives in the output directory, is
// often shared or committed, and --prune applies its paths without prompting;
// a "path": "../../..." entry must never reach os.Remove, os.Rename or a write.
func safeRelPath(outputDir, relPath string) (string, error) {
	local := filepath.FromSlash(relPath)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("unsafe snapshot path %q: must be local to output dir", relPath)
	}

	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve output dir %q: %w", outputDir, err)
	}
	abs := filepath.Join(absOutput, local)
	if abs != absOutput && !strings.HasPrefix(abs, absOutput+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe snapshot path %q: outside output dir", relPath)
	}
	return abs, nil
}
