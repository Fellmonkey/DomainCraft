# DomainCraft C# Persistence (Dapper) — `csharp-dapper`

> **Stub.** This is a placeholder for a Dapper-based persistence layer that will replace `csharp-efcore` via `--replace persistence=csharp-dapper`. It is not production-ready — it exists so the `--replace` contract has a second persistence to compose against.

## Status

- `extends: csharp-core` (not `csharp-domain` — Dapper may choose to reuse the domain layer or ship its own ports)
- `layer: persistence`
- Templates: skeleton, not a full Dapper repository implementation

## How it will compose

```bash
# when ready:
domaincraft generate --bridge csharp-rest --replace persistence=csharp-dapper
# → core + domain + dapper + rest
```

See [Axes and layers](https://domaincraft.github.io/domaincraft-site/docs/concepts/three-axes/) for how `--replace` swaps one edge of the chain.

## Docs

- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
