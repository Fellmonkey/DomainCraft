# ts-client TCK

The client half of the TypeScript certification: the thin `ts-core` is
certified once in `domaincraft-bridge-ts-core/tck/`; this suite certifies what
`ts-client` adds on top of it (hand-written oracle against `kitchen-sink.yaml`,
not generated from the IR).

| File | What it certifies |
|------|-------------------|
| `type-contract-client.ts` | Static contract (`tsc --noEmit`): Create/Update/Patch projections from `schemas.ts` — hidden settable on create, `password` excluded, `readonly` never settable, `version` on update/patch, merge-patch without enums/json/arrays. Negative cases via `@ts-expect-error`. |
| `runtime.client.test.ts` | Runtime (`vitest`, mocked fetch): `zodFieldErrors`, JWT client (`ApiError`: 409 concurrency vs unique, 429 retry/backoff honoring `Retry-After`), `PAGINATION`. |
| `interop.test.ts` | Runtime against a live k6-certified backend: CRUD, filter/sort/include accepted by the server, enum wire round-trip, error mapping. Skipped unless `API_URL` is set. |

The first two are backend-free (seconds in CI). The third is the cross-bridge
check (Docker); the CI `interop` job starts the certified csharp-restful
backend before running it.

## Run locally

```bash
# 1. generate kitchen-sink with ts-client (composes ts-core) into tck/generated
domaincraft generate \
  --domain <core>/compliance-suite/kitchen-sink.yaml \
  --bridge . \
  --output ./tck/generated \
  --non-interactive

# 2. certify (type + runtime; interop is skipped)
cd tck
npm install
npm run certify
```

### Interop

```bash
# 1. start a certified backend (csharp-restful) on :9000
domaincraft generate \
  --domain <core>/compliance-suite/kitchen-sink.yaml \
  --bridge ../domaincraft-bridge-csharp \
  --output ./test-app --non-interactive
(cd test-app && docker compose up -d --build)

# 2. run the interop suite
cd tck
API_URL=http://localhost:9000 npm run interop
```