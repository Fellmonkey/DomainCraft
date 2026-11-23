# DomainCraft Admin (`admin-alpine`)

**DomainCraft bridge that generates a static, self-hostable admin panel for any REST API your domain produces.**

Drop-in `domain.yaml`, and out comes a ready-to-serve HTML admin UI — dashboard, per-entity list and form pages — built with [Alpine.js](https://alpinejs.dev/) and [0build UIkit](https://0build.dev/). No Node tooling, no build step.

## Quick Start

```bash
# Generate the backend and its admin panel in one run
domaincraft generate --domain domain.yaml --bridge <any-backend-bridge> --admin

# Or point --admin directly at this bridge
domaincraft generate --domain domain.yaml --bridge <backend-bridge> --admin ../domaincraft-bridge-admin
```

The admin panel is written to `<output>/admin/` (only `docker-compose.override.yml` stays at
`<output>/` so Docker picks it up next to the backend's `docker-compose.yml`). Serve it with
any static server:

```bash
cd output/admin && python -m http.server 3000
```

Or via the bundled Docker override:

```bash
docker compose up admin   # http://localhost:3000
```

## What It Generates

| File | Purpose |
|------|---------|
| `index.html` | Dashboard — API health checks, entity quick-links, signed-in user |
| `<entity>.html` | Per-entity list: table, CSV export, bulk & single delete, row view |
| `<entity>-form.html` | Per-entity create/edit form |
| `login.html` / `register.html` | Auth pages — only when the project declares `auth` |
| `docker-compose.override.yml` | Serves `./admin` at port `3000` next to the API (stays at the output root) |

## Features

- **Full CRUD per entity** — list, create, row, edit, delete against the generated REST API.
- **Relation-aware forms** — many-to-one fields render as dropdowns populated from the related entity.
- **Type-aware inputs** — booleans → toggles, `array(...)` → comma textareas, `json`/`jsonb` → validated JSON textareas, `datetime` → date-time picker.
- **Validations reflected in the UI** — `required`, `email`, `url`, `min`/`max`, `gte`/`gt`/`lt`/`lte`, `regex` become HTML5 attributes and hints.
- **Enums** — enum-typed fields render as native selects from the project's enum definitions.
- **CSV export** + **bulk delete** + **bulk CSV download**.
- **JWT auth** — bearer token in `localStorage`, auto-redirect on 401. Only generated when auth is enabled.
- **Adjustable API URL** — set `window.API_BASE_URL` to point at any served backend.
- **Dark mode** — persisted light/dark toggle.

## How It Works

The bridge consumes the standard DomainCraft **IR** (entities, fields, relations, enums, features, auth/project settings) and generates one page set per entity. It talks to a plain REST API with the conventional shape:

```
GET    /api/{entity}        list
POST   /api/{entity}        create
GET    /api/{entity}/{id}   read
PUT    /api/{entity}/{id}   update
DELETE /api/{entity}/{id}   delete
POST   /api/{authEntity}/login  /register   (login/register only)
```

Any backend that exposes these endpoints — whatever bridge generated it — works.

## Requirements

- The **DomainCraft core** CLI (`domaincraft generate --admin ...`) to render these templates from your `domain.yaml`.
- A running backend with the REST endpoints above.
- A web browser only (Alpine.js, UIkit, ApexCharts load from CDNs).
