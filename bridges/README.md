# Bridges

Official DomainCraft bridge templates, kept next to the core so both ship from
one repository. Bridge IDs map to their paths via `internal/bridge/registry.go`.

## Layout

```
bridges/
  csharp/
    core/                       # csharp-core  (layer: core)
    domain/                     # csharp-domain (layer: domain)
    persistence/efcore/         # csharp-efcore (layer: persistence)
    persistence/dapper-stub/    # experimental stub, not in the registry
    transport/rest/             # csharp-rest   (layer: transport)
  typescript/
    core/                       # ts-core
    client/                     # ts-client
    react/                      # react-rest
  admin/                        # admin-alpine
  appwrite/                     # appwrite
```

## Working rules

- **One bridge per change** — a commit touches at most one bridge and names it.
- **Contract-first** — every bridge declares `contract_version` and must pass
  the bundled contract tests; after an intentional output change, run
  `make contract-golden-update`.
