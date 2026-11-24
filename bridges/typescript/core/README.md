# DomainCraft TypeScript Core — `ts-core`

The thin language core for TypeScript. Every TypeScript stack starts here.

`ts-core` compiles your `domain.yaml` into the only code every TypeScript stack needs — entity types, enum wire values, query schema and the permission matrix — and nothing else. No Zod, no fetch, no framework glue. Those live in the layers above.

## What it generates

Three templates, no opinions:

```
src/generated/
  ├── types.ts         # entity types, enums (strict snake_case wire values)
  ├── query.ts         # typed filter/sort/search/include DSL (path:op:value)
  └── permissions.ts   # ROLES + can()
```

## Why it is thin

The `core` holds the **contract** every TypeScript stack shares: enum wire values (`snake_case`), entity shapes, the `path:op:value` query grammar, the permission matrix. It is **certified once per language** by the per-language TCK in [`tck/`](./tck) — `type-contract.ts` (static `tsc --noEmit` with `Equal`/`@ts-expect-error`) + `runtime.test.ts` (vitest). It imports only what this layer generates (`types`/`query`/`permissions`). Every `client`/`framework` on top reuses it. Fix a wire bug once in `core`, and every combination inherits the fix — without re-certifying each adapter.

The layers above own the rest:

| Layer | Bridge | What it adds |
|---|---|---|
| `core` | `ts-core` | types, query, permissions (this repo) |
| `client` | `ts-client` | Zod schemas, JWT fetch client, CRUD |
| `framework` | `react-rest` | TanStack Query hooks, auth hooks, scaffold |

## How it composes

```yaml
# bridge.yaml
name: ts-core
layer: core
```

`ts-core` is the base of the TS chain. The layers above declare `extends: ts-core` (or `extends: ts-client`, which itself extends `ts-core`):

```bash
domaincraft generate --bridge ts-core --output ./out       # language core only (rare)
domaincraft generate --bridge ts-client --output ./out     # core + client (Zod + fetch + CRUD)
domaincraft generate --bridge react-rest --output ./out    # full: core → client → framework
```

## Try it

```bash
domaincraft generate --domain domain.yaml --bridge ts-core --output ./frontend
npm run build --prefix ./frontend
```

## Docs

- [Axes and layers](https://fellmonkey.github.io/domaincraft-site/docs/concepts/three-axes/)
- [The TypeScript client](https://fellmonkey.github.io/domaincraft-site/docs/guides/typescript-client/)
- [Query language](https://fellmonkey.github.io/domaincraft-site/docs/reference/query/)
- [Writing a bridge](https://fellmonkey.github.io/domaincraft-site/docs/guides/writing-a-bridge/)
- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
