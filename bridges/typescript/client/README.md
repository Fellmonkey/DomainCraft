# DomainCraft TypeScript Client — `ts-client`

The client layer. Validation, fetch and CRUD — on top of the language core.

`ts-client` builds on the thin `ts-core` and adds the code every TypeScript app actually imports — Zod schemas, a JWT fetch client and per-entity CRUD. No types (those come from `ts-core`), no hooks (those are `framework`).

## What it generates

Three templates on top of `ts-core`:

```
src/generated/
  ├── schemas.ts   # Zod create/update/patch schemas + zodFieldErrors
  ├── client.ts    # fetch wrapper — JWT, 429 retry/backoff, ApiError
  └── api.ts       # per-entity list/get/create/update/patch/delete
```

`types.ts`/`query.ts`/`permissions.ts` come from `ts-core` — not duplicated here.

## What makes this layer itself

**Certified layers-above-core.** `ts-core` is certified once by the per-language TCK in `domaincraft-bridge-ts-core/tck/`. `ts-client` certifies what *it* adds in its own [`tck/`](./tck/): `type-contract-client.ts` (static), `runtime.client.test.ts` (vitest, mocked fetch) and `interop.test.ts` — which proves the wire is accepted by a live `k6`-certified backend. The layer above (`react-rest`) does only a `tsc --noEmit` smoke against this certified core.

**Server-sourced validation.** The Zod schemas are compiled from the same IR as the API — `required`, `unique`, `email`, `url`, `min`/`max`, `gte`/`lte`, `regex`. A client cannot send a body the server would reject, because both sides share the contract.

## How it composes

```yaml
# bridge.yaml
name: ts-client
layer: client
extends: ts-core
```

```bash
domaincraft generate --bridge ts-client --output ./out     # core + client
domaincraft generate --bridge react-rest --output ./out   # full: core → client → framework
```

## Try it

```bash
domaincraft generate --domain domain.yaml --bridge ts-client --output ./frontend
npm run build --prefix ./frontend
```

## Docs

- [Axes and layers](https://domaincraft.github.io/domaincraft-site/docs/concepts/three-axes/)
- [The TypeScript client](https://domaincraft.github.io/domaincraft-site/docs/guides/typescript-client/)
- [Query language](https://domaincraft.github.io/domaincraft-site/docs/reference/query/)
- [Writing a bridge](https://domaincraft.github.io/domaincraft-site/docs/guides/writing-a-bridge/)
- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
