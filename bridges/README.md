# Bridges

The official DomainCraft bridges live in this tree. Each bridge is imported as
a single commit (no per-bridge git history) from its own repository; the
monorepo is the canonical source for the bundled versions.

The ID→path mapping lives in the registry (`internal/bridge/registry.go`),
never inferred from directory names. A bridge's `extends:` values stay **IDs**,
resolved through the registry to this tree.

## Layout

```
bridges/
  csharp/
    core/                       # csharp-core  (layer: core)
    domain/                     # csharp-domain (layer: domain)
    persistence/efcore/         # csharp-efcore (layer: persistence)
    persistence/dapper-stub/    # experimental stub, NOT in the registry
    transport/rest/             # csharp-rest   (layer: transport)
  typescript/
    core/                       # ts-core
    client/                     # ts-client
    react/                      # react-rest
  admin/                        # admin-alpine
  appwrite/                     # appwrite
```

> The deprecated `csharp-restful` monolith is intentionally **not** imported;
> its registry entry remains resolvable from cache/clone for compatibility.

## How to update a bridge

Re-import the bridge's current `main`:

```bash
git rm -r --quiet -- bridges/csharp/core
git rm -q --ignore-unmatch bridges/csharp/core
# then re-copy from the upstream repo and commit
```

Keep upgrades to **one bridge per commit** and re-run the bundled-bridges test
(`go test ./internal/renderer/ -run TestBundledBridgesLoad`).

## Notes

- Nested `.github/workflows/certification.yml` and `.gitignore`/`.gitattributes`
  are stripped on import — GitHub does not run nested workflows, and those files
  are monorepo noise. Per-bridge `LICENSE` and `README.md` are kept.
- Bridges are template packages, not Go modules: no `go.mod`, no `.go` files,
  so importing them does not affect the core Go build.
