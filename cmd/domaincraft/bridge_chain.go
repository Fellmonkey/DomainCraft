package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/bridgechain"
	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/interactive"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"github.com/spf13/cobra"
)

type commandContext struct {
	opts              *cliOptions
	cmd               *cobra.Command
	log               *logger.Logger
	chainHasCommunity bool
	cleanupApplied    bool
}

// newBridgeResolver builds a resolver with the CLI's bridge cache/update options.
func newBridgeResolver(opts *cliOptions, log *logger.Logger) *bridge.Resolver {
	return bridge.NewResolver(bridge.Default()).WithEnsureOptions(bridgeEnsureOptions(opts, log))
}

// resolveBridgeInteractive resolves the bridge from the --bridge flag, or
// prompts the user interactively. Returns (path, displayName, error).
func resolveBridgeInteractive(opts *cliOptions, log *logger.Logger) (string, string, error) {
	resolver := newBridgeResolver(opts, log)

	if opts.bridgePath != "" {
		// Monorepo-first, through the same step `extends` uses.
		if wd, err := os.Getwd(); err == nil {
			if p := bridge.ResolveOfficialLocal(opts.bridgePath, wd); p != "" {
				return p, "", nil
			}
		}
		resolved, err := resolver.Resolve(opts.bridgePath)
		return resolved, "", usageOnInvalidBridgeRef(err)
	}

	if opts.nonInteractive || !isTerminal() {
		return "", "", asUsageError(fmt.Errorf("--bridge is required in non-interactive mode"))
	}

	entry, err := interactive.SelectBridge(bridge.Default())
	if err != nil {
		return "", "", err
	}

	resolved, err := resolver.Resolve(entry.ID)
	if err != nil {
		return "", "", fmt.Errorf("resolve bridge %q: %w", entry.ID, err)
	}

	return resolved, entry.Name, nil
}

// resolveBaseChain resolves an adapter bridge's `extends` chain to local bridge
// paths, ordered outermost base first (so the deepest base renders first). Each
// base is resolved the same way --bridge is: a path, registry ID or owner/repo.
func resolveBaseChain(opts *cliOptions, adapterDir, baseRef string, log *logger.Logger) ([]string, error) {
	chain, err := bridgechain.Walk(baseRef, adapterDir, newBridgeResolver(opts, log), log)
	if err != nil {
		return nil, err
	}
	// The walker returns the deepest chain top-first; bases must render with
	// the outermost base first, so reverse it.
	slices.Reverse(chain)
	return chain, nil
}

// replaceEntry is one --replace flag: left (= layer or bridge name) -> right (bridge ref).
type replaceEntry struct {
	left  string
	right string
}

func parseReplaceFlags(raw []string) ([]replaceEntry, error) {
	var out []replaceEntry
	for _, s := range raw {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("invalid --replace %q: expected layer|bridgeId=bridgeRef", s)
		}
		out = append(out, replaceEntry{left: strings.TrimSpace(parts[0]), right: strings.TrimSpace(parts[1])})
	}
	return out, nil
}


// resolveChainWithReplacements resolves the extends chain with --replace overrides.
// It returns the (possibly replaced) entry dir and base paths outermost-first.
func resolveChainWithReplacements(opts *cliOptions, entryDir string, reps []replaceEntry, log *logger.Logger) (string, []string, error) {
	// One read per directory. A chain with N --replace flags used to parse
	// every manifest N+1 times through renderer.New and a second YAML reader.
	metaCache := make(map[string]bridge.Manifest)
	metaFor := func(dir string) (bridge.Manifest, error) {
		if m, ok := metaCache[dir]; ok {
			return m, nil
		}
		m, err := bridge.LoadManifest(dir)
		if err != nil {
			return bridge.Manifest{}, err
		}
		metaCache[dir] = m
		return m, nil
	}
	if len(reps) == 0 {
		m, err := metaFor(entryDir)
		if err != nil {
			return "", nil, err
		}
		bases, err := resolveBaseChain(opts, entryDir, m.Extends, log)
		return entryDir, bases, err
	}
	resolver := newBridgeResolver(opts, log)
	fullChain, err := bridgechain.Walk(entryDir, entryDir, newBridgeResolver(opts, log), log)
	if err != nil {
		return "", nil, err
	}
	for _, rep := range reps {
		// Find target index by layer or name.
		targetIdx := -1
		var targetLayer string
		for i, dir := range fullChain {
			m, err := metaFor(dir)
			if err != nil {
				return "", nil, err
			}
			if m.Layer == rep.left || m.Name == rep.left {
				targetIdx = i
				targetLayer = m.Layer
				break
			}
		}
		if targetIdx == -1 {
			return "", nil, asUsageError(fmt.Errorf("no bridge with layer or name %q in chain %v", rep.left, fullChain))
		}
		// Resolve replacement bridge.
		rightPath, err := bridge.ResolveExtendsRef(rep.right, entryDir, resolver)
		if err != nil {
			return "", nil, asUsageError(fmt.Errorf("resolve --replace %s=%s: %w", rep.left, rep.right, err))
		}
		if abs, err := filepath.Abs(rightPath); err == nil {
			rightPath = abs
		}
		rightMeta, err := metaFor(rightPath)
		if err != nil {
			return "", nil, err
		}
		// Warn on layer mismatch (both non-empty and different).
		if targetLayer != "" && rightMeta.Layer != "" && targetLayer != rightMeta.Layer && log != nil {
			log.Warn("layer mismatch: replacing %q (layer=%q) with %q (layer=%q)", rep.left, targetLayer, rightMeta.Name, rightMeta.Layer)
		}
		// Build replacement's full chain top-first.
		rightChain, err := bridgechain.Walk(rightPath, rightPath, newBridgeResolver(opts, log), log)
		if err != nil {
			return "", nil, err
		}
		// Splice: fullChain[:targetIdx] + rightChain (replaces tail after targetIdx)
		newChain := make([]string, 0, len(fullChain[:targetIdx])+len(rightChain))
		newChain = append(newChain, fullChain[:targetIdx]...)
		newChain = append(newChain, rightChain...)
		// Validate no duplicate layer and no cycle.
		seenLayer := make(map[string]string)
		seenPaths := make([]string, 0, len(newChain))
		for _, d := range newChain {
			abs, err := filepath.Abs(d)
			if err != nil {
				return "", nil, fmt.Errorf("resolve absolute bridge path %q: %w", d, err)
			}
			if slices.ContainsFunc(seenPaths, func(seen string) bool { return bridge.SamePath(seen, abs) }) {
				return "", nil, asUsageError(fmt.Errorf("bridge `extends` cycle detected at %q", d))
			}
			seenPaths = append(seenPaths, abs)
			m, err := metaFor(d)
			if err != nil {
				return "", nil, fmt.Errorf("read layer of %s: %w", d, err)
			}
			if m.Layer != "" {
				if prev, ok := seenLayer[m.Layer]; ok {
					return "", nil, asUsageError(fmt.Errorf("duplicate layer %q: %q and %q", m.Layer, prev, d))
				}
				seenLayer[m.Layer] = d
			}
		}
		fullChain = newChain
	}
	newEntry := fullChain[0]
	tail := fullChain[1:]
	slices.Reverse(tail)
	return newEntry, tail, nil
}

// --- domaincraft.lock.json (Phase 0.4) ---
//
// The lock records the exact bridge chain that rendered a project, next to the
// domain.yaml (not inside the output dir, not in the snapshot). It holds no
// timestamps, so it is diff-friendly and reproducible. It answers "which
// bridges rendered this project" — the snapshot answers "what did the model
// look like".

// lockBridge is one entry in domaincraft.lock.json.
type lockBridge struct {
	ID              string `json:"id"`
	Path            string `json:"path,omitempty"`   // monorepo path under bridges/ when resolved locally from the monorepo
	Source          string `json:"source,omitempty"` // GitHub owner/repo when resolved from the registry/cache
	Version         string `json:"version,omitempty"`
	ContractVersion int    `json:"contract_version"`
}

// domainLock is the on-disk shape of domaincraft.lock.json.
type domainLock struct {
	Bridges []lockBridge `json:"bridges"`
}

// writeDomainLock records the resolved bridge chain (top-first: entry, then its
// bases) next to domainPath. It is best-effort: a failure to write the lock
// must never break generation.
func writeDomainLock(domainPath string, dirs []string) error {
	reg := bridge.Default()
	var out []lockBridge
	seen := make(map[string]bool)
	for _, dir := range dirs {
		id, version, contractVersion, err := bridgeMetadata(dir)
		if err != nil {
			return err
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		b := lockBridge{ID: id, Version: version, ContractVersion: contractVersion}
		if entry := reg.ByID(id); entry != nil {
			if p := bridge.MonorepoRelPath(dir, entry.Path); p != "" {
				b.Path = p
			} else if entry.GitHub != "" {
				b.Source = entry.GitHub
			}
		}
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b lockBridge) int { return cmp.Compare(a.ID, b.ID) })

	return fsutil.WriteJSON(filepath.Join(filepath.Dir(domainPath), "domaincraft.lock.json"), domainLock{Bridges: out})
}

// bridgeMetadata is the lock-file view of the shared manifest reader.
func bridgeMetadata(dir string) (string, string, int, error) {
	m, err := bridge.LoadManifest(dir)
	if err != nil {
		return "", "", 0, err
	}
	return m.Name, m.Version, m.ContractVersion, nil
}

// bridgeEnsureOptions builds the bridge caching/update policy from the CLI
// flags and terminal state:
//   - --update-bridges consents to downloading newer versions (CI-safe);
//   - otherwise, an interactive terminal is prompted when an update is found;
//   - in non-interactive mode the cached copy is kept and a warning is logged.
func bridgeEnsureOptions(cli *cliOptions, log *logger.Logger) *bridge.EnsureOptions {
	opts := &bridge.EnsureOptions{
		Force: cli.updateBridges,
		Warn:  log.Warn,
	}

	switch {
	case cli.updateBridges:
		// The flag itself is the consent — update without prompting.
		opts.ConfirmUpdate = func(entry bridge.RegistryEntry, u *bridge.Update) (bool, error) {
			log.Info("%s", bridgeUpdateNotice(entry.ID, u))
			return true, nil
		}
	case !cli.nonInteractive && isTerminal():
		opts.ConfirmUpdate = func(entry bridge.RegistryEntry, u *bridge.Update) (bool, error) {
			return interactive.PromptBridgeUpdate(entry, u)
		}
	default:
		// Non-interactive (even on a TTY) without the flag: keep the cached
		// copy; the bridge package logs a warning pointing at --update-bridges.
		opts.ConfirmUpdate = nil
	}
	return opts
}

// bridgeUpdateNotice renders the log line for a bridge update. It deliberately
// omits version numbers: bridge.Update only carries the cached (old) version,
// which must not be presented as the available release.
func bridgeUpdateNotice(entryID string, _ *bridge.Update) string {
	return fmt.Sprintf("Updating bridge %s (cached copy is outdated)", entryID)
}

// runBridgeDatabaseMigrations applies the bridge's declared schema commands.
// They run with --migrate, or automatically under --prune when the diff has
// changes that must reach the database. Community bridges never auto-run them;
// explicit --migrate still needs --allow-bridge-commands consent. A failure is
// hard with --migrate and best-effort under --prune (the container can apply
// pending migrations at startup).
