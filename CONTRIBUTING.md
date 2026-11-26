# Contributing to DomainCraft

One bridge = **one layer in one axis**. This guide is for the core and for bridge authors — no Go required to add a language.

Full docs: [https://fellmonkey.github.io/domaincraft-site/docs/](https://fellmonkey.github.io/domaincraft-site/docs/) — start with [Writing a bridge](https://fellmonkey.github.io/domaincraft-site/docs/guides/writing-a-bridge/) and [Template context](https://fellmonkey.github.io/domaincraft-site/docs/reference/template-context/).

## Quick start

**Prerequisites:** Go 1.27+, Git

```bash
git clone https://github.com/DomainCraft/DomainCraft.git
cd DomainCraft/DomainCraft
make install-deps && make build   # → bin/domaincraft
make test && make lint
```

`make test` needs only Go. `make test-affected` additionally needs a working `python3` (or `python`) on `PATH` — the script uses it to walk the affected-package graph. Without it, run `make test`.

**Validate and generate:**

```bash
make cli-validate DOMAIN=examples/domain.yaml
make cli-generate DOMAIN=examples/domain.yaml BRIDGE=csharp-rest OUTPUT=/tmp/out
# direct:
go run ./cmd/domaincraft validate --domain domain.yaml
go run ./cmd/domaincraft generate --domain domain.yaml --bridge ../my-bridge --output ./out
# swap a layer without forking:
go run ./cmd/domaincraft generate --domain domain.yaml --bridge csharp-rest --replace persistence=../my-dapper --output ./out
```

Sibling `domaincraft-bridge-*` directories are legacy mirrors of the old standalone repos — they are not part of a fresh monorepo clone.

`--bridge` accepts a local path, a registry ID (`csharp-rest`, `ts-core`) or `owner/repo`. `--replace` accepts `layer=bridgeRef` or `bridgeId=bridgeRef` (repeatable). Create a starter `domain.yaml` by hand or in [Studio](https://fellmonkey.github.io/domaincraft-studio/) — an example is `examples/domain.yaml`.

## Architecture

```
domain.yaml → Parser → Lexer → Validator → IR Builder → Renderer → Generated Code
                                                      → Snapshot (.domaincraft/snapshot.json)
```

The **IR** is the contract. Templates read a fully linked `Project` — the core never contains C# or TypeScript. Composition is `extends` + `layer` + `--replace` (see [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/) and [Architecture](https://fellmonkey.github.io/domaincraft-site/docs/concepts/architecture/)).

## Creating a new bridge

A bridge is a directory with `bridge.yaml` + `type_mappings.yaml` + `templates/` — fully decoupled from the Go core.

### 1. Scaffold

```bash
mkdir my-bridge && cd my-bridge
```

### 2. `bridge.yaml`

```yaml
name: my-language-api
description: "My Language REST API bridge"
version: "1.0.0"
layer: persistence          # open ^[a-z][a-z0-9_]*$ — domain|persistence|transport for C#, core|framework|offline for web/mobile
extends: csharp-core        # optional base — path, registry ID or owner/repo
output_dir: generated

registry_url: "https://api.nuget.org/v3-flatcontainer/{id}/index.json"
registry_packages:
  ef_core: Microsoft.EntityFrameworkCore

migrations:
  enabled: true
  commands:
    - "dotnet ef migrations add InitialCreate --project src/Infrastructure ..."

templates:
  - for: entity
    source: templates/entity.go.tmpl
    target: "models/{{ .Entity.Name | snakecase }}.go"
  - for: project
    source: templates/enums.go.tmpl
    target: "models/enums.go"
    when: hasEnums
```

`for:` `entity` (per entity) or `project` (once). `when:` `hasSeed`/`hasEnums`/`hasOwnerTokens`/`hasAuth`/`hasMigration`/`hasMockData`/`hasAddon:dapr`. `overwrite: false` scaffolds once (developer-owned, protected by the snapshot engine). See [Writing a bridge](https://fellmonkey.github.io/domaincraft-site/docs/guides/writing-a-bridge/) for all fields.

Split **Core (regenerated)** vs **Custom (`overwrite: false`)** — put logic behind interfaces and scaffold once (partial classes in C#, base classes elsewhere). See [Generation Gap](https://fellmonkey.github.io/domaincraft-site/docs/concepts/generation-gap/).

### 3. `type_mappings.yaml`

Maps IR types to your language — the core stays language-agnostic:

```yaml
types:
  string: "string"
  uuid: "Guid"
value_types: ["int", "Guid"]
behaviors:
  cascade: "Cascade"
array_format: "List<%s>"
nullable_format: "?"
literals:
  uuid: { parse: "Guid.Parse(%s)", default: "Guid.NewGuid()" }
array:
  open: "new %s { "
  close: " }"
```

Every IR type must be mapped — missing types pass through as-is.

### 4. Templates

Go `text/template` with stdlib functions + bridge functions (`languageType`, `isValueType`, `deleteBehaviorName`, `literalValue`, `literalDefault`, `literalMember`, `arrayLiteralOpen`/`Close`, `columnSize`, `fkName`, `pluralize`, `pascalcase`, `snakecase` — see [Template context](https://fellmonkey.github.io/domaincraft-site/docs/reference/template-context/)).

Use `{{-`/`-}}` trimming. Filter feature fields with `.IsFeatureField`. Prefer `range .Entity.Fields` with `if` over pre-processing. See [Writing a bridge](https://fellmonkey.github.io/domaincraft-site/docs/guides/writing-a-bridge/) for conventions.

Available in templates: `.Project`, `.Entity`, `.Bridge`, `.Packages` — with IR helpers like `.HasAudit()`, `.IsEnum()`, `.IsUuid()`, `.EagerLoadNavigation()`, `.TableName()`, `AllFilterOperators()`.

### 5. Test

```bash
go run ./cmd/domaincraft generate --domain examples/domain.yaml --bridge ./my-bridge --output ./test-output
# then compile/lint the output in your target language
# for a layered bridge, test the composition and a replacement:
go run ./cmd/domaincraft generate --domain examples/domain.yaml --bridge ../domaincraft-bridge-csharp-rest --replace persistence=./my-bridge --output ./test-output
```

Add a CI job that generates from `examples/domain.yaml` (and `compliance-suite/kitchen-sink.yaml` for the TCK) and compiles the result. See [Certification](https://fellmonkey.github.io/domaincraft-site/docs/guides/certification/).

## Snapshot and migrations

After `generate` the IR + manifest is saved to `.domaincraft/snapshot.json`. The next run diffs it:

* **Deleted entities** — generated files removed; custom files prompt (or `--prune` for CI) — non-interactive without `--prune` only warns and keeps the old snapshot.
* **Renamed entities** — `old_name: Product` on `Item` renames custom files and emits `RenameTable`.
* **Type changes** — prints a manual-refactoring report.

See [Migrations](https://fellmonkey.github.io/domaincraft-site/docs/guides/migrations/).

## Core rules

* Keep the Go core language-agnostic — no C# / TypeScript in `internal/renderer`.
* DRY via `specmeta` — don't duplicate type/feature sets.
* Deterministic output — every map or set that affects files is iterated in sorted order via `slices.Sorted(maps.Keys(...))`, `slices.Sort` or `slices.SortFunc` with `cmp.Compare` (no raw `range` over maps). Two runs on the same `domain.yaml` produce identical bytes.
* Update docs and tests with code.

## Review checklist for bridge PRs

* `bridge.yaml` lists every file with correct `for:`/`when:`/`overwrite:` and `layer` (if layered) matches `^[a-z][a-z0-9_]*$` with no duplicate `layer` in the composed chain.
* `type_mappings.yaml` covers every IR type for the target.
* Output compiles for `examples/domain.yaml` (and `compliance-suite/kitchen-sink.yaml` for the TCK).
* `overwrite: false` only for scaffold-once files.
* `--prune` after a rename/delete cleans only the right files (check `.domaincraft/snapshot.json`).
* Deterministic output — two runs produce identical bytes (every map/set that affects files is iterated via `slices.Sorted` / `slices.Sort` / `slices.SortFunc` with `cmp.Compare`, no raw `range` over maps).

## Pull requests

- One change per pull request, with a title in the form **«Area: what changed (#issue)»** — for example «CI: gate e2e by paths (#12)». The first word is the area (CI, Core, Bridges, Studio, Docs…), then a third-person verb. Keep the description short — facts, no water.
- Anything larger than a small fix needs an agreed issue first — open one and wait before writing the code.
- **Commits are signed and verified** (GitHub shows "Verified"); unverified commits are not accepted.
- **An AI agent cannot be a contributor.** The author of a pull request is always a person, responsible for the change. AI may be used as a tool, but the description must state whether AI tools were used, which parts they wrote, and why.
- Every bug fix adds a check that fails without the fix and passes with it, and names it in `compliance-suite/regressions.tsv` with the issue or pull request and one line on what broke.
- Check determinism: two runs on the same `domain.yaml` produce identical bytes.
- By submitting a contribution you agree to the [Contributor License Agreement](CLA.md); participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).
