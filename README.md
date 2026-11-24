# DomainCraft

**DomainCraft is a domain-driven compiler. You describe your data model once in `domain.yaml`, and it compiles into consistent code for every layer of your product — API, web client, admin — through pluggable bridge templates.**

It replaces hand-written boilerplate: types, validation, queries, permissions, migrations and error handling come from the same IR, so layers cannot drift apart.

```yaml
# domain.yaml — that's all you write
project:
  name: MyShop
auth:
  type: jwt
  roles: [Admin, User]
entities:
  Product:
    features: [audit, soft_delete]
    fields:
      id: uuid [primary]
      title: string [required, min:3, max:200]
      price: decimal [required, gte:0]
      categoryId: relation(Category) [optional, on_delete:set_null]
    permissions:
      read: ["*"]
      create: [Admin]
      update: ["@Owner", Admin]
```

```bash
domaincraft generate --bridge csharp-rest
# → a complete, compilable project: entities, repositories, controllers, Docker, k8s
```

---

## Before you begin

You need Go 1.27 or later (to install from source) — or just a binary, no other dependencies.

## Step 1: Install

| How | Command |
|-----|--------|
| From source | `go install github.com/DomainCraft/DomainCraft/cmd/domaincraft@latest` |
| Binary | download from [GitHub Releases](https://github.com/DomainCraft/DomainCraft/releases) |

Then run your first generation:

```bash
domaincraft generate --bridge csharp-rest
```

---

## Quick start

Create `domain.yaml` by hand or in [Studio](https://fellmonkey.github.io/domaincraft-studio/) — a visual canvas with a Monaco editor, two-way sync.

```yaml
project:
  name: My App
database: postgresql
api_style: rest
auth:
  type: jwt
  roles: [Admin, User]
entities:
  User:
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      name: string [required]
      password: string [required, hidden]
```

```bash
domaincraft validate --domain domain.yaml          # check the model
domaincraft generate --bridge csharp-rest --output ./generated
cd generated && dotnet run --project src/WebApi    # run the API
```

The same model compiles to a typed web client:

```bash
domaincraft generate --bridge react-rest --output ./frontend
```

Swap a persistence layer without forking templates:

```bash
domaincraft generate --bridge csharp-rest --replace persistence=csharp-dapper
```

---

## Why DomainCraft?

| Hand-written | DomainCraft |
|--------------|-------------|
| 50+ files per entity by hand | 1 entity in ~10 lines of YAML |
| CRUD repeated per project | Generated consistently |
| Bugs fixed in every copy | Fixed in the template, fixed everywhere |
| New language = rewrite | New bridge, same domain |
| Permissions scattered | Declared with entities, wired end-to-end |

You describe *what* the domain looks like, not *how* to implement it.

## DomainCraft and AI

AI assistants are a strong tool for writing code, and most teams already use
them for boilerplate. That has costs: generated code is non-deterministic —
a re-run can change shape — and the API, the web client and the admin drift
apart because each is generated separately.

DomainCraft is not a competitor to that. It removes the part AI is worst at:

- **Deterministic.** Two runs on the same `domain.yaml` produce byte-identical
  output. No re-rolls, no surprises in review.
- **One contract.** Every layer comes from the same IR, so there is nothing to
  drift. Types, validation, queries, permissions and errors agree by
  construction.
- **Fewer tokens.** You describe the domain once, instead of prompting for
  each CRUD layer again. Tokens go to the parts that matter.

And it makes AI better where AI shines: screens are built by agents (or
humans) against the certified typed client — guaranteed types, permission
matrix and query grammar — so the agent builds UI instead of guessing. See
[Agent skills](https://fellmonkey.github.io/domaincraft-site/skills/).

---

## Features

| Area | What you get |
|------|--------------|
| **Types** | `string`, `int`, `bigint`, `float`, `decimal`, `boolean`, `date`, `datetime`, `uuid`, `text`, `json`/`jsonb`, `enum(Name)`, `array(Type)` — with traits `primary`, `required`, `unique`, `hidden`, `readonly`, validations `min`/`max`/`email`/`url`/`ipv4`/`regex`/`gte`/`lt`, defaults, and `on_delete: cascade\|set_null\|restrict\|no_action` |
| **Features** | `audit` (`createdAt`/`updatedAt`), `audit_log` (`createdBy`/`updatedBy`), `soft_delete` (`deletedAt`), `optimistic_lock` (`version` → `409 Conflict`), `event_sourced`, `cacheable` |
| **Permissions** | `read`/`create`/`update`/`delete` with `*` (public), roles (RBAC) and `@Owner` (ABAC) |
| **Bridges** | C# (domain → EF Core → REST), TypeScript (core → React), Appwrite, admin panel via `--admin` |
| **Migrations** | `old_name` renames become `RenameTable`/`RenameColumn` (data kept); `--prune` applies schema cleanup and runs bridge migrations best-effort |

Full language: [domain.yaml reference](https://fellmonkey.github.io/domaincraft-site/docs/reference/language/)

---

## How it works

```
domain.yaml → Parser → Lexer → Validator → IR Builder → Renderer → Generated Code
                                                      → Snapshot (.domaincraft/snapshot.json)
```

The **IR** is the contract. It is language-agnostic — the core never contains C# or TypeScript — and every bridge renders from it. Two runs on the same `domain.yaml` produce byte-identical output.

### Axes and layers

One bridge = one layer in one axis. All bridges live in this repository (`bridges/`) and are embedded into the binary:

```
backend (C#):  domain (core) → persistence (efcore/dapper) → transport (rest/grpc)
web:           core (ts-core) → framework (react/vue)
mobile:        core (dart-core) → framework (flutter) → offline (drift)
```

`extends:` composes a chain; `--replace` swaps one edge without forking:

```
--replace persistence=dapper   → core+dapper+rest
--replace transport=grpc       → core+efcore+grpc
```

A new persistence or transport layer is one more bridge in `bridges/` — not a fork of the templates, and not a new repository.

---

## Bridges

| ID | Layer | Targets |
|-----|-------|--------|
| `csharp-core` | `domain` | Domain + Application |
| `csharp-efcore` | `persistence` — `extends: csharp-core` | EF Core + PostgreSQL + `migrations:` |
| `csharp-rest` | `transport` — `extends: csharp-efcore` | ASP.NET REST + JWT + k8s/tests |
| `csharp-restful` | — | *Archived* monolith — use `csharp-rest` |
| `ts-core` | `core` | TypeScript data layer (types, Zod, query DSL, permissions) |
| `react-rest` | `framework` — `extends: ts-core` | TanStack Query + auth |
| `appwrite` | — | Appwrite TablesDB (`appwrite.config.json`) |
| `admin-alpine` | — | Static admin (`--admin`) |

Official bridges ship in `bridges/` and are embedded into the binary as the default; `--update-bridges` refreshes them with a contract-version gate. Community bridges are cached in `~/.domaincraft/bridges/`. Full registry: [Bridges](https://fellmonkey.github.io/domaincraft-site/docs/reference/bridges/).

---

## CLI

```bash
domaincraft generate --domain domain.yaml --bridge csharp-rest --output ./generated
domaincraft validate --domain domain.yaml
domaincraft bridges --check-updates
domaincraft update --check
```

Flags: `--domain`/`-d`, `--bridge`/`-b`, `--output`/`-o`, `--admin`, `--replace left=right` (repeatable), `--prune`/`--migrate`, `--addons dapr`, `--non-interactive`. See [CLI reference](https://fellmonkey.github.io/domaincraft-site/docs/reference/cli/).

---

## Migration

The IR and manifest are saved to `.domaincraft/snapshot.json`. On the next run, renames (`old_name: Product`) become `RenameTable`/`RenameColumn` (data kept), deletions prompt, type changes print a report. `--prune` applies cleanup and rewrites identifiers in `overwrite: false` files. See [Migrations](https://fellmonkey.github.io/domaincraft-site/docs/guides/migrations/).

---

## Certification

The compliance suite (`compliance-suite/`) is a hand-written oracle: `kitchen-sink.yaml` (a maximal domain) + `suite.js` (k6 over HTTP). TypeScript ships its own TCK in `ts-core/tck/`. See [Certification](https://fellmonkey.github.io/domaincraft-site/docs/guides/certification/).

---

## Documentation

- [Docs](https://fellmonkey.github.io/domaincraft-site/docs/)
- [Contributing](CONTRIBUTING.md)

---

## Limitations

Generated C# targets ASP.NET Core + EF Core + PostgreSQL today. Other stacks come as new bridges — the core never changes for them.

---

## License

[Apache-2.0](./LICENSE). Third-party licences are in [NOTICE](./NOTICE).
