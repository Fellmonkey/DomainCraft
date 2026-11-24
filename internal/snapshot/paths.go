package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// safeRelPath resolves a snapshot file path against outputDir and rejects
// anything that escapes it. The snapshot lives in the output directory, is
// often shared or committed, and --prune applies its paths without prompting;
// a "path": "../../..." entry must never reach os.Remove, os.Rename or a write.
//
// The lexical check alone is not enough: --prune follows symlinks. Every
// existing component is therefore resolved with EvalSymlinks and must stay
// inside the resolved output root. The returned path keeps the lexical form so
// callers see paths under outputDir even when that directory is itself a link.
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

	realOutput, err := filepath.EvalSymlinks(absOutput)
	if err != nil {
		return "", fmt.Errorf("resolve output dir %q: %w", outputDir, err)
	}
	rel, err := filepath.Rel(absOutput, abs)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot path %q: %w", relPath, err)
	}
	cur, realCur := absOutput, realOutput
	for comp := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if comp == "" || comp == "." {
			continue
		}
		cur = filepath.Join(cur, comp)
		realComp, err := filepath.EvalSymlinks(cur)
		if errors.Is(err, os.ErrNotExist) {
			// The component (and everything below it) has not been created yet.
			continue
		}
		if err != nil {
			return "", fmt.Errorf("resolve snapshot path %q: %w", relPath, err)
		}
		if realComp != realCur && !strings.HasPrefix(realComp, realCur+string(filepath.Separator)) {
			return "", fmt.Errorf("unsafe snapshot path %q: follows a symlink outside output dir", relPath)
		}
		realCur = realComp
	}
	return abs, nil
}
