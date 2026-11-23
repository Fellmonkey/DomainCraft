# DomainCraft React — `react-rest`

The framework layer. TanStack Query hooks and auth — on top of the certified client.

`react-rest` is a thin adapter over `ts-client` (which builds on `ts-core`). It declares `extends: ts-client`, so `ts-core` generates the language core (types, query, permissions), `ts-client` adds Zod, fetch and CRUD, and `react-rest` adds only the React glue: TanStack Query hooks and auth hooks.

## What it generates

Three templates on top of `ts-client`:

```
src/
  ├── hooks.ts    # TanStack Query: useX, useXs, useCreateX, ...; keyset useInfinite* for int-PK
  └── auth.ts     # login/register/setup/me + useLogin/useCan role guard
package.json, tsconfig.json, index.ts, README.md   # scaffold
```

`types.ts`/`query.ts`/`permissions.ts` come from `ts-core`; `schemas.ts`/`client.ts`/`api.ts` from `ts-client` — not duplicated here.

## Replace, not fork

```bash
# default: core → client → react-rest
domaincraft generate --bridge react-rest

# Vue or Svelte instead of React? Ship one framework repo:
domaincraft generate --bridge react-rest --replace framework=vue-rest
# → core + client + vue-rest
```

One framework variant = one repo. See [Axes and layers](https://domaincraft.github.io/domaincraft-site/docs/concepts/three-axes/).

## How it composes

```yaml
# bridge.yaml
name: react-rest
layer: framework
extends: ts-client
```

```bash
domaincraft generate --bridge react-rest --output ./frontend     # full: core → client → framework
```

The `extends: ts-client` (and `ts-client → ts-core`) chain resolves via the registry cache, or via a monorepo sibling checkout (`../domaincraft-bridge-ts-*`) during development.

## Try it

```bash
domaincraft generate --domain domain.yaml --bridge react-rest --output ./frontend
npm install --prefix ./frontend && npm run build --prefix ./frontend
```

## Docs

- [Axes and layers](https://domaincraft.github.io/domaincraft-site/docs/concepts/three-axes/)
- [The TypeScript client](https://domaincraft.github.io/domaincraft-site/docs/guides/typescript-client/)
- [Writing a bridge](https://domaincraft.github.io/domaincraft-site/docs/guides/writing-a-bridge/)
- [DomainCraft](https://github.com/DomainCraft/DomainCraft)
