# DomainCraft C# Persistence (Dapper) — `csharp-dapper-stub`

> **Stub.** A placeholder for a Dapper-based persistence layer. It lets the `--replace` contract compose a second persistence from the repository root. Not production-ready — templates are a skeleton, not a full Dapper repository.

## Status

- `extends: csharp-domain` (as declared in `bridge.yaml`; the stub reuses the domain layer)
- `layer: persistence`
- Not in `internal/bridge/registry.go` — pass its local path, not an ID

## How it composes

```bash
# from the repository root
domaincraft generate --bridge csharp-rest --replace persistence=bridges/csharp/persistence/dapper-stub
# → core → domain → dapper-stub → rest
```

See [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/) for how `--replace` swaps one edge of the chain.

## Docs

- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
