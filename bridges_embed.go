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
// Go excludes files whose names begin with "_" when embedding a directory.
// Bridge helper templates use that prefix, so each of them is listed explicitly
// below. TestEmbeddedBridgesMatchTree compares this set with the source tree.
//
//go:embed bridges
//go:embed bridges/csharp/core/templates/_helpers.cs.tmpl
//go:embed bridges/typescript/core/templates/_helpers.ts.tmpl
//go:embed bridges/admin/templates/_helpers.html.tmpl
//go:embed bridges/csharp/persistence/dapper-stub/templates/_helpers.cs.tmpl
var Bridges embed.FS
