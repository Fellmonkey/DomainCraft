# DomainCraft C# Core — `csharp-core`

The thin language core for C#. Every C# stack starts here.

`csharp-core` compiles your `domain.yaml` into the only code every C# stack needs — entity shapes, enum wire values, query schema and the permission matrix — and nothing else. No `DbContext`, no services, no controllers. Those live in the layers above.

## What it generates

Four templates, no opinions:

```
src/Domain/Entities/<Entity>.g.cs      # POCOs
src/Domain/Shared/Enums.g.cs           # enums with strict snake_case wire values
src/Domain/Shared/PagedResult.g.cs     # paged list contract
src/Application/Query/Query.g.cs       # QuerySchema (path:op:value grammar)
```

## Why it is thin

The `core` holds the **contract** every C# stack shares: enum wire values (`snake_case`), entity shapes, `QuerySchema`, permission matrix, `TableName`/`ColumnName`. It is **certified once per language** by the per-language TCK in [`tck/`](./tck) — a hand-written xUnit project that compiles the generated sources (the build itself is a contract check) and proves wire values, the JSON converter, entity type surface and the query grammar. Every `persistence`/`transport` on top reuses it. Fix a wire bug once in `core`, and every combination inherits the fix — without re-certifying each adapter.

The layers above own the rest:

| Layer | Bridge | What it adds |
|---|---|---|
| `core` | `csharp-core` | entities, enums, query, permissions (this repo) |
| `domain` | `csharp-domain` | services (Generation Gap), `IRepository` ports, security |
| `persistence` | `csharp-efcore` | `DbContext`, repositories, migrations, Dapr |
| `transport` | `csharp-rest` | controllers, JWT, tests, Docker, k8s |

## How it composes

```yaml
# bridge.yaml
name: csharp-core
layer: core
```

`csharp-core` is the base of the C# chain. The layers above declare `extends: csharp-core` (or `extends: csharp-domain`, which itself extends `csharp-core`):

```bash
domaincraft generate --bridge csharp-core --output ./out      # language core only (rare)
domaincraft generate --bridge csharp-domain --output ./out     # core + domain
domaincraft generate --bridge csharp-rest --output ./out       # full: core → domain → efcore → rest
```

## Try it

```bash
domaincraft generate --domain domain.yaml --bridge csharp-core --output ./generated
dotnet build ./generated/src/Domain
```

## Docs

- [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/) — how bridges compose in one repository
- [Writing a bridge](https://fellmonkey.github.io/domaincraft-site/docs/guides/writing-a-bridge/)
- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
