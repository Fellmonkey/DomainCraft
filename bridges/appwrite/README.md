# domaincraft-bridge-appwrite

Turn your `domain.yaml` into a complete Appwrite backend schema.

Appwrite already gives you everything else: schema-as-code (`appwrite.config.json`),
the Appwrite CLI v26 as a migration engine (`appwrite push`), built-in auth, teams,
row-level security, and SDKs. The one stage you still do by hand is **translating
your domain model into that schema** — clicking through the Console or hand-writing
the config. This bridge closes that stage.

> Appwrite already automates some of this, but only **one table at a time**: AI
> suggestions generate a single table's columns and indexes from its name, and
> Full Schema Creation provisions one table per request. Neither models
> **relations, permissions, roles, and seed across the whole project** as one
> versionable, declarative model — that's the layer this bridge adds.

**domain as schema → schema as code → Appwrite.** You model the domain once,
declaratively; the bridge compiles it to `appwrite.config.json` and the CLI deploys it.

## What it is

- **Input** — `domain.yaml`: entities, fields, relations, permissions, roles, seed data.
- **Output** — a single `appwrite.config.json` describing the whole backend
  (database, tables, columns, relationships, indexes, permissions, role teams,
  project settings), plus a few runtime helpers.

| File | Purpose |
|---|---|
| `appwrite.config.json` | The entire schema — tables, columns, relationships, indexes, permissions, roles, settings |
| `helpers/permissions.js` | Row-level permissions for `@Owner` (generated only when used) |
| `scripts/seed.js` | Idempotent seed from the model's `seed:` block (when present) |
| `README.md` | Deployment guide + this project's limitation matrix |

**What it does NOT do:** generate server code or migration scripts. Your app
(client, function, or server) talks to Appwrite directly through the SDK. The
bridge prepares the schema and permissions; the **Appwrite CLI v26 is the
migration engine** — it diffs the config against your live project and brings it
into line.

## Why

- **No Console click-work.** Model once in YAML, generate the whole schema.
- **One source of truth.** Schema, permissions, and roles all derive from `domain.yaml`.
- **No hand-written migrations.** Change the model → regenerate → `appwrite push`
  computes and applies the diff.
- **Permissions can't drift.** `permissions` compile to table `$permissions`, roles
  to teams (`role-admin`, `role-user`), `@Owner` to row-level security.

## Quick start

```bash
# 1. Bind to your project (once)
appwrite client --endpoint https://cloud.appwrite.io/v1 --project-id <PROJECT_ID> --key <API_KEY>

# 2. Generate the schema
domaincraft generate --domain domain.yaml --bridge domaincraft-bridge-appwrite --output .

# 3. Deploy (re-running is a no-op)
appwrite push all --force

# 4. Seed (only when the model has seed)
node scripts/seed.js
```

`--migrate` chains 2 + 3: `domaincraft generate … --migrate` — bind first (step 1),
or the push has no project to target.

> `appwrite client` lives in the CLI session and never touches the config file, so
> generating is always safe. Don't run `appwrite init project` — it clobbers the
> schema. No API key? `appwrite login`, then set `projectId`/`endpoint` in the file.

> ⚠️ `push` treats the config as the **full** project state — databases/tables not
> present in the config will be proposed for deletion. Don't push into a project
> with data outside your domain model.

## How it works

```
domain.yaml ──(domaincraft generate)──▶ appwrite.config.json ──(appwrite push)──▶ Appwrite
```

1. `domain.yaml` — the domain model (the same DSL used by every DomainCraft bridge).
2. `appwrite.config.json` — a declarative description of the *desired* Appwrite
   project state.
3. `appwrite push all` — the Appwrite CLI diffs desired vs. actual and applies the
   plan. The bridge emits no migration scripts.

## Key concepts

- **Auth is native Appwrite.** The auth entity (e.g. `User`) is **not** created as
  a table — users live in Appwrite Auth. Relations to it become `varchar(36)`
  columns holding user IDs.
- **Roles → teams.** Each role in `auth.roles` becomes a team `role-<role>`. After
  registration, add the user to a team:
  `teams.createMembership("role-user", [], email)`.
- **`@Owner` → row security.** When creating a row, pass `rowPermissions("orders", userId)`
  from `helpers/permissions.js` — Appwrite then filters `list` by row permissions
  automatically.
- **Seed.** `SEED_USER_ID=<userId> node scripts/seed.js` (idempotent) — `@Owner` row
  permissions resolve to that user.

## Renames & data safety

| Change in `domain.yaml` | What `push` does | Data |
|---|---|---|
| field `old_name` | renames the column in place (`previousKey`) | preserved |
| entity `old_name` | keeps the table `$id`, updates its `name` | preserved |
| `required` / `default` / `size` / `min` / `max` / `elements` / `onDelete` | updates the column in place | preserved |
| `type` / `array` / `relatedTable` | recreates the column | **lost** (plan warns) |

**Entity rename** (`old_name`) pins the table `$id` to the old name, so `push`
updates the display `name` in place and rows survive. `old_name` is a *permanent*
`$id` pin: removing it changes the `$id` → delete+create → move rows manually
before pushing.

⚠️ Appwrite ≤1.9.5: renaming a column on a table with relationship columns fails
(index-rebuild bug). Workaround: recreate the table, or temporarily drop the
relationship → push → restore it.

Server: use `appwrite/appwrite:1.9.5` (1.9.6/`latest` were broken at the time of
writing — `exec format error`).

## Limitations

Rule: only what can be expressed in `appwrite.config.json` is implemented;
everything else is app-side logic or no-op fields.

| DomainCraft construct | In Appwrite | Severity |
|---|---|---|
| `decimal` | `float`/`double` (floating point — precision loss; store money as integer cents in `int`) | 🔴 |
| `json` / `jsonb` | `text` (JSON string) | 🟠 |
| `date` | `datetime` ISO 8601 (a time appears) | 🟠 |
| self-relation | `varchar(36)` parent ID, no FK (Appwrite issue #9471) | 🟠 |
| `default` on a required field | `required:false` + default (Appwrite forbids default on required) — column becomes nullable | 🟡 |
| audit/soft_delete/optimistic_lock features | columns with `required:false` — server doesn't auto-fill; the app writes them | 🟡 |
| indexes on relationship columns | dropped from the index (TablesDB refuses); empty index omitted | 🟡 |
| `default: now()` / `uuid()` | static defaults only | 🟡 |
| `regex` / `hidden` / `readonly` | validation/semantics in the app | 🟡 |
| `gt` / `lt` (strict) | approximated by inclusive `min`/`max` | 🟡 |
| `on_delete: no_action` | `restrict` | 🟡 |
| roles with same lowercase slug | unsupported (`role-<lowercase>` — one team) | 🟡 |
| index type (btree/gin/…) | `key` (key/unique/fulltext only) | 🟡 |
| `event_sourced` / `cacheable` / `multi_tenancy` / `infrastructure` | ignored | ⚪ |

## Certification

The bridge is certified by its own workflow (not the shared compliance-suite
template — that one targets bridges generating a runnable app; this one pushes a
schema).

`tck/verify.js` is an independent, hand-written oracle: it pushes the config to a
live Appwrite project and checks the **actual server state** against the mapping
contract (tables, rowSecurity, `$permissions`, columns, indexes, role teams,
settings, seed, idempotency, rename round-trip). The oracle is not generated from
the bridge templates, so a template bug can't "green" the test.

Verified against Appwrite Cloud: **132/132** (kitchen-sink) and **38/38** (gaps) —
CERTIFIED. Both modes run in one `tck` project with one key (the oracle cleans up
stray role teams itself). CI: `.github/workflows/certification.yml` (requires an
API key in the `APPWRITE_TCK_KEY` secret).

```bash
node tck/verify.js --out ./tck-out --project tck --key 'standard_...' \
  --endpoint https://cloud.appwrite.io/v1
# gaps mode: add --gaps --out ./tck-gaps-out --gaps-pre ./tck-gaps-pre-out
```

Design and support map: `appwrite-bridge-plan.md` (DomainCraft repo root).

## Example

A complete Demo Shop output lives in [`example/`](example/): the source
`domain.yaml` plus the generated config, helpers, seed, and README.

```bash
domaincraft generate --domain example/domain.yaml \
  --bridge domaincraft-bridge-appwrite --output example --non-interactive
```
