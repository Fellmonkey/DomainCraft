# DomainCraft C# REST — `csharp-rest`

The transport layer. From domain to a running API in one command.

`csharp-rest` completes the stack `core → domain → efcore → rest` into a production-ready ASP.NET Core API. You write `domain.yaml`; it writes the plumbing — controllers, JWT, health checks, tests, Docker, k8s.

## Replace, not fork

```bash
# default: core → domain → efcore → rest
domaincraft generate --bridge csharp-rest
cd generated && dotnet run --project src/WebApi
# → entities, controllers, auth, health checks, Swagger, rate limiting, Docker — done

# gRPC or Minimal API instead of REST? Ship one transport repo:
domaincraft generate --bridge csharp-rest --replace transport=csharp-grpc
# → core + domain + efcore + grpc
```

One transport variant = one repo. At `10×10` that is 22 repos for 100 combinations; at `30×30` — 62 instead of 900. See [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/).

## What it adds on top of `csharp-efcore`

20 templates:

```
src/WebApi/
  ├── Controllers/<Entity>Controller.g.cs          # per entity, auth via PermissionPlan
  ├── Controllers/<Auth>Controller.g.cs            # when: hasAuth — login/register/me/setup
  ├── Extensions/ServiceCollectionExtensions.g.cs   # AddWebApiServices/AddWebApiLogging
  ├── Extensions/ApplicationBuilderExtensions.g.cs   # UseWebApiPipeline/MapWebApiEndpoints
  ├── HealthChecks/HealthCheckResponseWriter.g.cs
  └── Program.cs                                    # overwrite:false — you own the entry point
appsettings.json / appsettings.Development.json / Staging / Production
tests/
  ├── ApiTests.g.cs / AuthTests.g.cs
  ├── EntityTests.g.cs / EntityStateTests.g.cs      # xUnit + WebApplicationFactory + Testcontainers (real PG)
  └── <Project>.Tests.csproj
Dockerfile / docker-compose.yml / deploy/k8s/{deployment,service,ingress}.yaml
<Project>.sln / src/WebApi/WebApi.csproj
```

Every list endpoint is `?page=&pageSize=&sort=-field&q=` with `PagedResult<T>`. `PATCH` is merge-patch via `System.Text.Json` (no Newtonsoft). Controllers never touch `DomainDbContext` — they talk to `I<Entity>Service`.

## Day-2 safe

`Program.cs` is `overwrite: false` and delegates to regenerated `ServiceCollectionExtensions.g.cs` / `ApplicationBuilderExtensions.g.cs` — your pipeline tweaks survive every `generate`. Add a field, regenerate: only `.g.cs` files change; your `Program.cs` and custom service partials are untouched.

## Tested like you would

`EntityTests` asserts the full status-code contract (`200/201/204/400/401/403/404/409/429`), `@Owner` isolation, `optimistic_lock` `409`, seed data and validations — the same TCK that certifies the monolith. Tests use `WebApplicationFactory` + Testcontainers (real PostgreSQL, real FK/cascade/SQL).

## How it composes

```yaml
# bridge.yaml
name: csharp-rest
layer: transport
extends: csharp-efcore
```

```bash
domaincraft generate --bridge csharp-rest --output ./out
domaincraft generate --bridge csharp-rest --replace persistence=csharp-dapper --output ./out
```

`templates`/`helpers`/`type_mappings.yaml` are merged base-first, the adapter wins. `migrations:` is the first `enabled` top-down (so `rest` without its own migrations finds `efcore`).

## Try it

```bash
domaincraft generate --domain domain.yaml --bridge csharp-rest --output ./generated
dotnet test ./generated/tests
docker compose -f ./generated/docker-compose.yml up --build
```

## Docs

- [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/)
- [Authentication](https://fellmonkey.github.io/domaincraft-site/docs/guides/authentication/)
- [Permissions](https://fellmonkey.github.io/domaincraft-site/docs/guides/permissions/)
- [Deploying the generated API](https://fellmonkey.github.io/domaincraft-site/docs/guides/deploy/)
- [CLI reference](https://fellmonkey.github.io/domaincraft-site/docs/reference/cli/)
- [Writing a bridge](https://fellmonkey.github.io/domaincraft-site/docs/guides/writing-a-bridge/)
- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
