# ts-core TCK

Per-language TCK for TypeScript: a hand-written oracle against
`kitchen-sink.yaml` certifying the thin `ts-core` (types / query / permissions).
Written by hand against the contract — not generated from the IR, or a bridge
bug would reproduce itself in the test and pass.

| File | What it certifies |
|------|-------------------|
| `type-contract.ts` | Static contract (`tsc --noEmit`): enum wire values, `hidden`/`readonly`/`password` exclusions, `T \| null` vs `T \| undefined`, per-type filter operators, include projections. Negative cases via `@ts-expect-error`. |
| `runtime.test.ts` | Runtime (`vitest`): `encodeFilter` → exact `path:op:value` string, `buildQueryParams`, `can()` against the permission matrix. |

Imports only what ts-core generates (types/query/permissions). Layers above are
certified in `domaincraft-bridge-ts-client/tck/` (client runtime + interop
against a live k6-certified backend); `react-rest` adds only a `tsc --noEmit`
glue smoke.

## Run locally

```bash
domaincraft generate \
  --domain <core>/compliance-suite/kitchen-sink.yaml \
  --bridge . \
  --output ./tck/generated \
  --non-interactive
cd tck
npm install
npm run certify   # tsc --noEmit && vitest run
```