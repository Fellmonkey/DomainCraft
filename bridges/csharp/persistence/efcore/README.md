# DomainCraft C# Persistence — `csharp-efcore`

The persistence layer. EF Core, PostgreSQL, repositories and migrations — wired to your domain.

`csharp-efcore` builds on `csharp-domain` (which builds on `csharp-core`) and adds how your entities hit the database. It is one layer, one repo — replace it with a flag, not a fork of 71 templates.

## Replace, not fork

```bash
# default: core → domain → efcore → rest
domaincraft generate --bridge csharp-rest

# Dapper instead of EF Core:
domaincraft generate --bridge csharp-rest --replace persistence=csharp-dapper
# → core + domain + dapper + rest (dapper extends csharp-core)
```

One persistence variant = one repo. At `10×10` that is 22 repos for 100 combinations; at `30×30` — 62 instead of 900. See [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/).

## What it adds on top of `csharp-domain`

17 templates:

```
src/Infrastructure/
  ├── Persistence/
  │   ├── DomainDbContext.g.cs
  │   ├── Configurations/<Entity>Configuration.g.cs     # IEntityTypeConfiguration<T>
  │   ├── Seeders/DomainSeeder.g.cs                     # when: hasSeed
  │   └── Migrations/SchemaRenames.g.cs                # when: hasMigration — RenameTable/RenameColumn
  ├── Repositories/<Entity>Repository.g.cs
  ├── Security/BcryptPasswordHasher.g.cs                # when: hasAuth
  ├── Caching/InMemoryCacheService.g.cs     DaprCacheService.g.cs     (hasAddon:dapr)
  ├── Email/InMemoryEmailService.g.cs       DaprEmailService.g.cs
  ├── Storage/LocalFileStorageService.g.cs   DaprStorageService.g.cs
  └── Events/InMemoryEventPublisher.g.cs    DaprEventPublisher.g.cs
src/Infrastructure/Infrastructure.csproj
```

## Database migrations

Declared in `bridge.yaml`:

```yaml
migrations:
  enabled: true
  commands:
    - "dotnet ef migrations add InitialCreate --project src/Infrastructure --startup-project src/WebApi"
    - "dotnet ef database update --project src/Infrastructure --startup-project src/WebApi"
```

Run with `--migrate` (or `--prune`, which is best-effort and non-blocking — in Docker the container migrates on startup via `Database:SchemaMode=migrate`).

**Renames keep their data.** When a field or entity carries an `old_name` hint, `SchemaRenames.g.cs` emits the exact `RenameTable`/`RenameColumn` statements — EF Core would otherwise drop and recreate, losing rows.

## Ports stay ports

`IRepository`, `IEventPublisher`, `ICacheService`, `IEmailService`, `IStorageService` are defined in `csharp-domain`. This layer provides `InMemory*` (default) or `Dapr*` (`--addons dapr`) + `dapr/components/*.yaml`. Swap the broker/cache/store by editing the manifests — your code never changes, no vendor lock-in.

## No transport leakage

No controller writes `Microsoft.EntityFrameworkCore` — `csharp-rest` does. If the transport needs persistence, it goes through a shared helper or a fragment. The layer is not leaky.

## How it composes

```yaml
# bridge.yaml
name: csharp-efcore
layer: persistence
extends: csharp-domain
```

```bash
domaincraft generate --bridge csharp-efcore --output ./out     # core + domain + persistence
domaincraft generate --bridge csharp-rest --output ./out       # full 4-layer stack
```

## Try it

```bash
domaincraft generate --domain domain.yaml --bridge csharp-efcore --output ./generated
dotnet build ./generated/src/Infrastructure
```

## Docs

- [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/)
- [Schema migrations](https://fellmonkey.github.io/domaincraft-site/docs/guides/migrations/)
- [Infrastructure & addons](https://fellmonkey.github.io/domaincraft-site/docs/guides/infrastructure-addons/)
- [Writing a bridge](https://fellmonkey.github.io/domaincraft-site/docs/guides/writing-a-bridge/)
- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
