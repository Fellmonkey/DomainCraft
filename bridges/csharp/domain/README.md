# DomainCraft C# Domain — `csharp-domain`

The backend domain layer. Business logic, isolated from the database and the API.

`csharp-domain` builds on the thin `csharp-core` and adds the code you actually own — application services with the Generation Gap, repository ports, security and infrastructure ports. No `DbContext`, no controllers — those are `persistence` and `transport`.

## What it adds on top of `csharp-core`

22 templates:

```
src/Application/
  ├── Services/
  │   ├── ServiceHooks.g.cs                      # HookResult / HookException
  │   ├── I<Entity>Service.g.cs                  # service interfaces
  │   ├── Generated/<Entity>Service.g.cs         # regenerated CRUD + hooks (partial)
  │   └── <Entity>Service.cs                      # overwrite:false — your hooks (partial)
  ├── Services/ (auth)
  │   ├── IAuthService.g.cs + Generated/AuthService.g.cs + AuthService.cs   # when: hasAuth
  ├── Security/
  │   ├── IPermissionService.g.cs + PermissionService.g.cs
  │   ├── IOwnerResolver.g.cs + OwnerResolver.g.cs
  │   └── IPasswordHasher.g.cs (impl in persistence)
  ├── Caching/ICacheService.g.cs
  ├── Email/IEmailService.g.cs
  ├── Storage/IStorageService.g.cs
  ├── Events/IEventPublisher.g.cs                # ports — impl in persistence
  └── MockData.g.cs                               # when: hasMockData
src/Domain/Domain.csproj
src/Application/Application.csproj
docs/Entities/<Entity>.md + ProjectSummary.md
```

## Generation Gap — your hooks survive every regenerate

Each entity gets a `partial class` service split across two files:

| File | Behavior |
|---|---|
| `Generated/<Entity>Service.g.cs` | **Regenerated every run.** CRUD logic, soft-delete, and async hooks: `OnBefore*Async` (return `HookResult.Fail("reason")` → HTTP 400), `OnAfter*Async` (post-commit), `On*OverrideAsync` (return `HookResult.Handled()` to replace the whole flow). |
| `Services/<Entity>Service.cs` | **Generated once** (`overwrite: false`), owned by you. Override the hooks to add business logic. |

```csharp
// src/Application/Services/ProductService.cs — never overwritten
public partial class ProductService
{
    protected override async Task<HookResult> OnBeforeCreateAsync(Product entity, CancellationToken ct)
    {
        var ok = await Services.GetRequiredService<IExternalPricingApi>().ValidateAsync(entity.Price, ct);
        return ok ? HookResult.Success() : HookResult.Fail("Price is not acceptable");
    }
}
```

`Services.GetRequiredService<T>()` resolves external dependencies — no constructor surgery. `HookResult` supports `Success()` (continue), `Handled()` (skip persistence) and `Fail(reason)` (abort → 400).

## Ports, not implementations

`IRepository`, `ICacheService`, `IEmailService`, `IStorageService`, `IEventPublisher` are **ports** defined here. The `persistence` layer picks the implementation: `InMemory*` (default) or `Dapr*` (`--addons dapr`). The domain never knows which.

## How it composes

```yaml
# bridge.yaml
name: csharp-domain
layer: domain
extends: csharp-core
```

```bash
domaincraft generate --bridge csharp-domain --output ./out     # core + domain
domaincraft generate --bridge csharp-efcore --output ./out     # + persistence
domaincraft generate --bridge csharp-rest --output ./out       # full 4-layer stack
```

## Try it

```bash
domaincraft generate --domain domain.yaml --bridge csharp-domain --output ./generated
dotnet build ./generated/src/Application
```

## Docs

- [Axes and layers](https://domaincraft.github.io/domaincraft-site/docs/concepts/three-axes/)
- [Generation Gap](https://domaincraft.github.io/domaincraft-site/docs/concepts/generation-gap/)
- [Writing a bridge](https://domaincraft.github.io/domaincraft-site/docs/guides/writing-a-bridge/)
- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
