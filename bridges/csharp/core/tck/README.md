# csharp-core TCK (Technology Compatibility Kit)

The **per-language TCK** for the C# family — a hand-written certification suite
for the `csharp-core` bridge. It is written **against the DomainCraft
contract**, not generated from the same IR as the bridge, so a bridge bug
cannot be reproduced in the test and pass.

## What it certifies — csharp-core alone

This TCK certifies **only the thin `csharp-core` layer** (Entities / Enums /
PagedResult / Query). It compiles the generated sources directly into the test
project, so **the build itself is a contract check** — a template that stops
emitting valid C# fails `dotnet build` before a single assertion runs. It never
touches `DbContext`, repositories, services or controllers — those belong to
`csharp-domain` / `csharp-efcore` / `csharp-rest`.

| File | Kind | What it certifies |
|------|------|-------------------|
| `WireContractTests.cs` | runtime (`dotnet test`) | Enum wire values are core-owned `snake_case` (`[EnumMember]`), the generated JSON converter emits the wire value (not the CLR member name) and rejects unknown values, entity type surface (uuid → `Guid`, int PK → `int`, `bigint` → `long`, `float` → `double`, `decimal` → `decimal`), `T?` vs required references, FK columns + navigations, collections for `[many]`, `[JsonIgnore]` on `hidden`/`password`, and feature fields (`audit`, `audit_log`, `soft_delete`, `optimistic_lock`). |
| `QueryContractTests.cs` | runtime (`dotnet test`) | The `path:op:value` grammar of spec/query.md: `FilterParser` (and/or/in, json vs jsonb root operator sets, rejection of unknown fields/operators), `FilterExpression` LINQ translation (numeric, enum wire values, one-hop relation paths, json text, jsonb ordered), the sort grammar (`SortParser`/`SortApplier`), `ListQuery` normalization (`limit` wins, clamping, restrict-list `include`) and `PagedResult` math. |

Layers above the core are certified separately: `csharp-domain` +
`csharp-efcore` + `csharp-rest` form the server chain certified by the backend
`k6 suite.js` (see `DomainCraft/compliance-suite/`). The C# TCK has no
interop half — the wire-format *acceptance* proof for C# is that same k6 suite.

## Run locally

```bash
# 1. generate kitchen-sink with csharp-core into tck/generated
domaincraft generate \
  --domain <core>/compliance-suite/kitchen-sink.yaml \
  --bridge . \
  --output ./tck/generated \
  --non-interactive

# 2. build + test (no database, no network beyond nuget restore)
cd tck
dotnet test
```

## Independence rule

The test files are maintained by hand against `kitchen-sink.yaml` (project
`TestPlatform`). Do **not** generate them from the IR — that would make the
oracle share the bridge's mistakes.