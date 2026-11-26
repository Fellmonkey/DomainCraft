// Package domaincraft is the root package of the DomainCraft module. It exists
// solely to embed the bundled bridge trees so the CLI can generate code without
// a network round-trip (no clone required for official bridges).
package domaincraft

import "embed"

// Bridges is the embedded copy of the monorepo `bridges/` tree. The loader uses
// it as the fallback before cloning from a remote (local path → embedded →
// remote → cache).
//
// `go:embed bridges` compiles the tree into the binary at build time. A release
// needs no regeneration step: checkout at the tag plus `go build` embeds exactly
// that commit's templates.
//
// Helper templates are named helpers.*.tmpl (no "_" prefix), so a single
// directory directive embeds them along with every other bridge file.
// TestEmbeddedBridgesMatchTree compares this set with the source tree.
//
//go:embed bridges
var Bridges embed.FS
