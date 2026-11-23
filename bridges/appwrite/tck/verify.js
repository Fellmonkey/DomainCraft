#!/usr/bin/env node
// DomainCraft Appwrite bridge — certification oracle (round-trip).
//
// Verifies the *mapping contract* (plan §5–§6) against a live Appwrite
// instance — Appwrite Cloud in CI (this repo hosts no Appwrite infra).
// Hand-written on purpose: if it were generated from the same templates as
// the bridge, a bridge bug would be reproduced in the test.
//
// Both modes share ONE project: `push all` already deletes tables absent from
// the config (deleteRemovedTables) but NEVER deletes stale teams (the teams
// push is a flat create/update-only resource — no deleteRemovedTeams in CLI,
// no upstream issue asking for one), so the oracle cleans role teams itself
// before each run (cleanupRoleTeams) to keep exact team counts assertable.
//   default — kitchen-sink (14 tables, @Owner/rowSecurity, relationships,
//             teams, settings, seed)
//   --gaps  — tck/appwrite-gaps.yaml (appwrite-only contracts): the entity-
//             rename $id-pin (old id + new name, plan §3.2), the relatedTable
//             pin, bigint gte/lte bounds, no-permissions-block → any, empty
//             permission lists → deny. Same project, exact counts.
//
// Authenticates with a project API key (standard CI pattern — Appwrite Cloud
// has no CLI email/password login). The key and its project must exist
// beforehand (console → API Keys; all scopes). The oracle patches the
// generated config's projectId/endpoint, pushes, then asserts server state.
//
//   node tck/verify.js [--out ./tck-out] [--key 'standard_…'] \
//       [--endpoint https://cloud.appwrite.io/v1] [--gaps] \
//       [--gaps-pre ./tck-gaps-pre-out]
//
// --gaps-pre pushes a PRE-rename config first (entity still called Gadget)
// and seeds its row, so the post-rename push (Widget, old_name: Gadget)
// demonstrably preserves the table's rows — the "строки на месте" proof of
// the $id-pin (plan §3.2).
"use strict";

const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const CLI = process.env.APPWRITE_CLI || "appwrite";

// NOTE for future edits: any status-checked `run()` call site must also call
// throwIfTransient(res) so cloud network hiccups retry instead of becoming
// assertion failures. runJson throws on any nonzero exit already.
function run(args, opts = {}) {
  const res = spawnSync(CLI, args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, ...opts });
  if (res.error) throw res.error;
  return res;
}
function runOk(args, opts = {}) {
  const r = run(args, opts);
  return r.status === 0 ? r.stdout : "";
}
function runJson(args, opts = {}) {
  const r = run(args, opts);
  if (r.status !== 0) throw new Error(`CLI failed: ${args.join(" ")}\n${r.stdout}\n${r.stderr}`);
  const text = r.stdout.trim();
  const start = text.indexOf("{");
  if (start < 0) throw new Error(`No JSON in output of: ${args.join(" ")}\n${text}`);
  return JSON.parse(text.slice(start));
}
function sleep(ms) { Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms); }

// `push all --force` is idempotent — a transient failure (worker drain,
// cloud hiccup) needs only one retry with a pause.
function pushAllRetry(cwd) {
  let res;
  for (let attempt = 1; attempt <= 3; attempt++) {
    res = run(["push", "all", "--force"], { cwd });
    if (res.status === 0) break;
    if (!/timed out|Server Error|timeout/i.test(res.stdout + res.stderr)) break;
    if (attempt < 3) { console.log(`   … push attempt ${attempt} failed — waiting 10s and retrying`); sleep(10000); }
  }
  return res;
}

// CLI's teams push never deletes stale teams (flat create/update-only
// resource), so role teams from a previous mode would pollute exact-count
// asserts in a shared project. Delete role-* teams outside the expected set.
function cleanupRoleTeams(expected) {
  const teamsRes = runJson(["teams", "list", "--json"]);
  const stale = (teamsRes.teams || [])
    .map((t) => t.$id)
    .filter((id) => id.startsWith("role-") && !expected.includes(id));
  for (const id of stale) {
    const r = run(["teams", "delete", "--team-id", id, "--force"]);
    if (r.status !== 0) console.log(`   ⚠ could not delete stale team ${id}: ${(r.stderr || r.stdout).slice(0, 120)}`);
  }
  if (stale.length) console.log(`   ✓ removed ${stale.length} stale role team(s): ${stale.join(", ")}`);
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------
let checks = 0;
let failures = 0;
function check(name, ok, detail = "") {
  checks++;
  console.log(`${ok ? "✅" : "❌"} ${name}${ok ? "" : " — " + detail}`);
  if (!ok) failures++;
}

// A NONZERO CLI exit whose detail smells like a transient network hiccup
// (TLS handshake, timeout, connection errors) must THROW, not count as an
// assertion failure — runWithRetry only retries on throws, and the run is
// idempotent. Only consulted when status !== 0: a successful call's stdout
// may legitimately contain words like "Server Error" or uuids like 550e….
function throwIfTransient(res) {
  if (res.status === 0) return;
  const detail = (res.stdout + res.stderr).slice(-300);
  if (/timed out|timeout|TLS handshake|ECONN|dial tcp|connection refused/i.test(detail)) {
    throw new Error(`transient CLI failure: ${detail.slice(0, 160)}`);
  }
}
function section(name) { console.log(`\n── ${name} ──`); }

// Per-mode expectations (mapping contract, plan §5–§6 + §3.2 for the pin)
const KITCHEN_SINK = {
  tables: ["Categories", "Counters", "Roles", "Tags", "EventLogs", "Folders", "Documents",
    "Orders", "Payments", "Products", "OrderItems", "ProductTags", "Reviews", "UserProfiles"],
  // Tables whose primary key is NOT a uuid (row $id): the PK is a real column
  // (Counter.id int [primary] → integer column + unique index, plan §4).
  nonUuidPk: ["Counters"],
  ownerTables: ["Orders", "OrderItems", "Reviews", "Documents", "Folders", "UserProfiles"],
  expectRelationship: {
    Products: ["categoryId"],
    OrderItems: ["orderId", "productId"],
    ProductTags: ["productId", "tagId"],
    Reviews: ["productId"],
    Documents: ["folderId"],
    Payments: ["orderId"],
  },
  authLinks: {
    Categories: ["parentId"], Products: ["supplierId"], Orders: ["userId"],
    Reviews: ["userId"], Documents: ["authorId"], Folders: ["parentId", "ownerId"],
    UserProfiles: ["userId"], EventLogs: ["userId"],
  },
  roleTeams: ["role-admin", "role-manager", "role-editor", "role-user", "role-viewer"],
  seedTable: "products",
  seedRowId: "550e8400-e29b-41d4-a716-446655440000",
  seedDataLabel: "DB default filled (viewCount)",
  seedDataCheck: (row) => row.viewCount === 0,
};

const GAPS = {
  tables: ["Widgets", "Bundles", "PublicNotes", "LockedNotes"],
  // The renamed entity keeps the pinned $id "gadgets" (plan §3.2); the unpinned
  // "widgets" id must NOT exist. perms modes: owner (create-only @Owner),
  // public (no permissions block → all ops any), deny (empty lists → no grants).
  pinned: [
    { id: "gadgets", name: "Widgets", rowSecurity: true, perms: "owner" },
    // template kebabcase splits CamelCase with a hyphen: PublicNotes → public-notes
    { id: "public-notes", name: "PublicNotes", rowSecurity: false, perms: "public" },
    { id: "locked-notes", name: "LockedNotes", rowSecurity: false, perms: "deny" },
  ],
  unpinnedId: "widgets",
  expectRelationship: { Bundles: ["widgetId"] },
  authLinks: {},
  roleTeams: ["role-admin", "role-user"],
  seedTable: "gadgets",
  seedRowId: "550e8400-e29b-41d4-a716-446655440111",
  seedDataLabel: "seed data intact (stock=42)",
  seedDataCheck: (row) => row.stock === 42,
};

// Parse "create(\"team:role-admin\")" → { create: ["team:role-admin"], ... }
function parsePerms(perms) {
  const ops = {};
  for (const p of perms) {
    const m = p.match(/^(create|read|update|delete)\("?(.*?)"?\)$/);
    if (m) (ops[m[1]] = ops[m[1]] || []).push(m[2]);
  }
  return ops;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
function parseArgs(argv) {
  const opts = {
    out: "./tck-out",
    project: "tck",
    key: "",
    endpoint: "https://cloud.appwrite.io/v1",
    gaps: false,
    gapsPre: "",
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--out") opts.out = argv[++i];
    else if (a === "--project") opts.project = argv[++i];
    else if (a === "--key") opts.key = argv[++i];
    else if (a === "--endpoint") opts.endpoint = argv[++i];
    else if (a === "--gaps") opts.gaps = true;
    else if (a === "--gaps-pre") opts.gapsPre = argv[++i];
  }
  return opts;
}

async function main() {
  // Fresh counters per attempt — runWithRetry re-invokes main on transient
  // failures and the globals must not accumulate across runs.
  checks = 0;
  failures = 0;
  const opts = parseArgs(process.argv.slice(2));
  const key = opts.key;
  if (!key) {
    console.error("✗ no API key — pass --key");
    process.exit(1);
  }
  const outDir = path.resolve(opts.out);
  const configPath = path.join(outDir, "appwrite.config.json");
  const endpoint = opts.endpoint.replace(/\/+$/, "");
  const spec = opts.gaps ? GAPS : KITCHEN_SINK;

  if (!fs.existsSync(configPath)) {
    console.error(`✗ ${configPath} not found — run domaincraft generate first`);
    process.exit(1);
  }

  // Isolated CLI state so the TCK never touches the developer's config.
  process.env.APPWRITE_HOME = fs.mkdtempSync(path.join(require("os").tmpdir(), "tck-home-"));

  // --- 1. Client with API key ----------------------------------------------
  section("1. Client & key");
  const client = run(["client", "--endpoint", endpoint, "--project-id", opts.project,
    "--key", key, "--force"]);
  throwIfTransient(client);
  check("client configured with key", client.status === 0,
    (client.stdout + client.stderr).slice(0, 200));
  if (client.status !== 0) process.exit(1);

  // --- 1b. Reset role teams (shared project) --------------------------------
  // Both modes share one project; `push all` never deletes stale teams, so
  // remove role-* teams left by a previous mode before pushing this one.
  section("1b. Cleanup role teams");
  cleanupRoleTeams(spec.roleTeams);

  // --- 2. Patch generated config with real project/endpoint -----------------
  const cfg = JSON.parse(fs.readFileSync(configPath, "utf8"));
  cfg.projectId = opts.project;
  cfg.endpoint = endpoint;
  fs.writeFileSync(configPath, JSON.stringify(cfg, null, 2));

  // --- 3. Push all (with retry) --------------------------------------------
  section("2. Push all");
  if (opts.gaps && opts.gapsPre) {
    // Rename round-trip (plan §3.2): push the PRE-rename config first and seed
    // its row, so the post-rename push demonstrably preserves table rows.
    const preDir = path.resolve(opts.gapsPre);
    const preCfgPath = path.join(preDir, "appwrite.config.json");
    if (!fs.existsSync(preCfgPath)) {
      check("gaps-pre config exists", false, `${preCfgPath} missing (--gaps-pre)`);
    } else {
      const preCfg = JSON.parse(fs.readFileSync(preCfgPath, "utf8"));
      preCfg.projectId = opts.project;
      preCfg.endpoint = endpoint;
      fs.writeFileSync(preCfgPath, JSON.stringify(preCfg, null, 2));
      const pre = pushAllRetry(preDir);
      throwIfTransient(pre);
      check("pre-rename push exit 0", pre.status === 0, (pre.stdout + pre.stderr).slice(-300));
      const preSeed = path.join(preDir, "scripts", "seed.js");
      if (pre.status === 0 && fs.existsSync(preSeed)) {
        const s = spawnSync(process.execPath, [preSeed], { cwd: preDir, encoding: "utf8" });
        throwIfTransient(s);
        check("pre-rename seed exit 0", s.status === 0, (s.stdout + s.stderr).slice(-300));
        if (s.status === 0) {
          const rows = runJson(["tablesdb", "list-rows", "--json", "--database-id", "main", "--table-id", spec.seedTable]);
          const arr = rows.rows || rows.documents || [];
          check("row exists BEFORE rename", arr.some((d) => d.$id === spec.seedRowId),
            JSON.stringify(arr[0] || {}).slice(0, 120));
        }
      }
    }
  }
  const push = pushAllRetry(outDir);
  throwIfTransient(push);
  check("push all exit 0", push.status === 0, (push.stdout + push.stderr).slice(-400));
  if (push.status !== 0) {
    console.error("\n✗ push failed after retries — no point asserting state");
    process.exit(1);
  }

  // --- 4. Assert server state against the mapping contract ------------------
  section("3. Tables");

  // 3.1 table set (exact count — push already deleted tables not in the config)
  const dbs = runJson(["tablesdb", "list-tables", "--json", "--database-id", "main"]);
  const tables = dbs.tables || [];
  const names = tables.map((t) => t.name).sort();
  check(`${spec.tables.length} tables (${tables.length})`, tables.length === spec.tables.length, names.join(", "));
  const missing = spec.tables.filter((n) => !names.includes(n));
  check("table names match model", missing.length === 0, "missing: " + missing.join(", "));

  // One get-table per table carries $permissions + columns + (via follow-up)
  // indexes — richer than list-tables, and it is what push itself reconciles.
  const full = {}; // keyed by table $id — the server identity (pins survive renames)
  for (const t of tables) {
    full[t.$id] = runJson(["tablesdb", "get-table", "--json", "--database-id", "main", "--table-id", t.$id]);
  }

  // 3.2/3.3 table rowSecurity + $permissions
  if (opts.gaps) {
    // Gaps: $id-pin + per-table permission mode (plan §3.2, §5)
    for (const p of spec.pinned) {
      const got = full[p.id];
      check(`pin ${p.id}: table exists`, !!got, `no table with $id ${p.id}`);
      if (!got) continue;
      check(`pin ${p.id}: name is new (${p.name})`, got.name === p.name, `got ${got.name}`);
      check(`pin ${p.id}: rowSecurity ${p.rowSecurity}`, got.rowSecurity === p.rowSecurity, `got ${got.rowSecurity}`);
      const ops = parsePerms(got.$permissions || []);
      if (p.perms === "owner") {
        check(`pin ${p.id}: create-only (owner)`,
          !!ops.create && ops.create.length > 0 && !ops.read && !ops.update && !ops.delete,
          JSON.stringify(got.$permissions));
      } else if (p.perms === "public") {
        const all = Object.values(ops).flat();
        check(`pin ${p.id}: all ops any (no permissions block)`,
          all.length === 4 && ["create", "read", "update", "delete"].every((op) => ops[op] && ops[op].length === 1 && ops[op][0] === "any"),
          JSON.stringify(got.$permissions));
      } else if (p.perms === "deny") {
        check(`pin ${p.id}: empty $permissions (deny)`, (got.$permissions || []).length === 0,
          JSON.stringify(got.$permissions));
      }
    }
    const unpinned = tables.find((t) => t.$id === spec.unpinnedId);
    check(`no unpinned ${spec.unpinnedId} table`, !unpinned, unpinned ? `found $id ${unpinned.$id}` : "");
  } else {
    // Kitchen-sink: rowSecurity exactly on @Owner entities; $permissions mapped
    for (const t of tables) {
      const isOwner = spec.ownerTables.includes(t.name);
      const got = full[t.$id].rowSecurity === true;
      check(`rowSecurity ${t.name} (${isOwner})`, got === isOwner,
        `got ${full[t.$id].rowSecurity}, want ${isOwner}`);
    }
    for (const t of tables) {
      const perms = full[t.$id].$permissions || [];
      const isOwner = spec.ownerTables.includes(t.name);
      const ops = parsePerms(perms);
      const all = Object.values(ops).flat();
      const badForm = all.filter((g) => g !== "any" && g !== "users" && !g.startsWith("team:role-"));
      check(`$permissions ${t.name}: forms valid`, badForm.length === 0,
        JSON.stringify(perms));
      if (isOwner) {
        check(`$permissions ${t.name}: create-only (owner)`,
          !!ops.create && ops.create.length > 0 && !ops.read && !ops.update && !ops.delete,
          JSON.stringify(perms));
      } else {
        check(`$permissions ${t.name}: all 4 ops mapped`,
          !!ops.create && !!ops.read && !!ops.update && !!ops.delete,
          JSON.stringify(perms));
      }
    }
  }

  section("4. Columns");
  const featureCols = ["createdAt", "updatedAt", "deletedAt", "version", "createdBy", "updatedBy"];
  // Non-auth relationships → `relationship` columns; links to the auth entity
  // (User) and self-references → plain varchar(36) user/parent ids (plan §6).
  for (const t of tables) {
    const cols = (full[t.$id].columns || []).filter((c) => !c.key.startsWith("$"));
    const keys = cols.map((c) => c.key);
    // uuid PK → row $id (no column); non-uuid PK (spec.nonUuidPk) → real
    // column, required, covered by a unique index (plan §4)
    if ((spec.nonUuidPk || []).includes(t.name)) {
      const id = cols.find((c) => c.key === "id");
      check(`${t.name}: non-uuid PK column id required`, !!id && id.required === true,
        JSON.stringify(id || {}).slice(0, 100));
    } else {
      check(`${t.name}: no uuid-PK column`, !keys.includes("id"), keys.join(","));
    }
    // Non-auth relations are real Appwrite relationships
    for (const k of spec.expectRelationship[t.name] || []) {
      const c = cols.find((x) => x.key === k);
      check(`${t.name}.${k} is relationship`,
        !!c && c.type === "relationship" && !!c.relatedTable,
        JSON.stringify(c || {}).slice(0, 140));
    }
    // auth links (User) and self-refs are varchar(36)
    for (const k of spec.authLinks[t.name] || []) {
      const c = cols.find((x) => x.key === k);
      check(`${t.name}.${k} is varchar(36)`,
        !!c && c.type === "varchar" && !c.relatedTable,
        JSON.stringify(c || {}).slice(0, 140));
    }
    // Feature columns: audit/soft_delete/optimistic_lock fields must be
    // nullable (Appwrite does not fill them server-side — the app does).
    for (const c of cols.filter((x) => featureCols.includes(x.key))) {
      check(`${t.name}.${c.key} nullable`, c.required === false, JSON.stringify(c).slice(0, 100));
    }
  }
  if (opts.gaps) {
    // Widgets.stock: bigint with gte:1 → min=1, lte:1000000 → max=1000000
    const stock = (full["gadgets"].columns || []).find((c) => c.key === "stock");
    check("Widgets.stock type bigint", !!stock && stock.type === "bigint", JSON.stringify(stock || {}).slice(0, 120));
    if (stock) {
      check("Widgets.stock min=1 (gte)", stock.min === 1, `got ${stock.min}`);
      check("Widgets.stock max=1000000 (lte)", stock.max === 1000000, `got ${stock.max}`);
      check("Widgets.stock required", stock.required === true, `got ${stock.required}`);
    }
    // Bundles.widgetId → relationship pinned to the OLD id (plan §3.2)
    const wcol = (full["bundles"].columns || []).find((c) => c.key === "widgetId");
    check("Bundles.widgetId relatedTable pinned", !!wcol && wcol.relatedTable === "gadgets",
      `got ${wcol && wcol.relatedTable}`);
  }

  section("5. Indexes");
  // Index columns are only returned by the RAW output (`-R`); the filtered
  // `--json` drops them. The forbidden set is exact: the uuid PK (row $id, no
  // column) plus the relationship columns of that table. Auth links (userId,
  // supplierId, …) and plain string columns (Payment.transactionId) are
  // legitimately indexable and must NOT be flagged by a suffix heuristic.
  for (const t of tables) {
    const idxRes = runJson(["tablesdb", "list-indexes", "-R", "--database-id", "main", "--table-id", t.$id]);
    const idx = idxRes.indexes || [];
    const forbidden = [...(spec.nonUuidPk || []).includes(t.name) ? [] : ["id"],
      ...(spec.expectRelationship[t.name] || [])];
    for (const i of idx) {
      const cols = i.columns || [];
      const bad = cols.filter((c) => forbidden.includes(c));
      check(`${t.name} idx ${i.key}: no relation/uuid-PK columns`,
        bad.length === 0, `cols=${JSON.stringify(cols)} forbidden=${JSON.stringify(forbidden)}`);
    }
  }

  section("6. Teams");
  const teamsRes = runJson(["teams", "list", "--json"]);
  const roleTeams = (teamsRes.teams || [])
    .filter((t) => t.$id.startsWith("role-"))
    .map((t) => t.$id)
    .sort();
  const expectedRoles = [...spec.roleTeams].sort();
  check("role team ids", JSON.stringify(roleTeams) === JSON.stringify(expectedRoles),
    `${roleTeams.join(",")} vs ${expectedRoles.join(",")}`);

  section("7. Settings");
  // settings push is additive — email-password must be enabled
  const projInfo = run(["project", "get", "--json"]);
  throwIfTransient(projInfo);
  check("project get ok", projInfo.status === 0, (projInfo.stdout + projInfo.stderr).slice(0, 150));
  try {
    const pj = JSON.parse(projInfo.stdout.slice(projInfo.stdout.indexOf("{")));
    const authMethods = pj.authMethods || [];
    const email = (authMethods.find((m) => m.$id === "email-password") || {}).enabled === true;
    check("email-password auth enabled", email, JSON.stringify(authMethods).slice(0, 150));
  } catch (e) {
    check("email-password auth enabled", false, "no authMethods in project get");
  }

  // --- 5. Idempotency -------------------------------------------------------
  section("8. Idempotency");
  const push2 = run(["push", "all", "--force"], { cwd: outDir });
  throwIfTransient(push2);
  const noop = /no changes|already up to date|up to date|nothing to/i.test((push2.stdout + push2.stderr).toLowerCase());
  check("second push is a no-op", push2.status === 0 && noop,
    (push2.stdout + push2.stderr).slice(-300));

  // --- 6. Seed ---------------------------------------------------------------
  section("9. Seed");
  const seedPath = path.join(outDir, "scripts", "seed.js");
  if (fs.existsSync(seedPath)) {
    const seed = spawnSync(process.execPath, [seedPath], { cwd: outDir, encoding: "utf8" });
    throwIfTransient(seed);
    check("seed.js exit 0", seed.status === 0, (seed.stdout + seed.stderr).slice(-300));
    if (seed.status === 0) {
      const rows = runJson(["tablesdb", "list-rows", "--json", "--database-id", "main", "--table-id", spec.seedTable]);
      const arr = rows.rows || rows.documents || [];
      const seeded = arr.find((d) => d.$id === spec.seedRowId);
      check("seed row id matches uuid PK", !!seeded, JSON.stringify(arr[0] || {}).slice(0, 120));
      if (seeded) check(spec.seedDataLabel, spec.seedDataCheck(seeded),
        `got ${JSON.stringify(seeded).slice(0, 160)}`);
    }
  } else {
    check("seed.js generated", false, `no scripts/seed.js — ${opts.gaps ? "gaps" : "kitchen-sink"} model has seed`);
  }

  // --- Summary ---------------------------------------------------------------
  console.log(`\n${"=".repeat(60)}`);
  console.log(`Checks: ${checks}  Passed: ${checks - failures}  Failed: ${failures}`);
  if (failures > 0) {
    console.log("✗ NOT CERTIFIED");
    process.exit(1);
  }
  console.log("✓ CERTIFIED — Appwrite bridge honors the DomainCraft mapping contract");
}

// The run is idempotent (push no-op, seed skips existing rowIds), so a
// transient cloud/network failure mid-asserts can be recovered by one rerun.
// Real assertion failures exit(1) inside main and are NOT retried.
async function runWithRetry() {
  for (let attempt = 1; attempt <= 2; attempt++) {
    try {
      await main();
      return;
    } catch (e) {
      if (attempt === 2) throw e;
      console.log(`\n⚠ transient failure (${e.message}) — rerunning in 10s…`);
      sleep(10000);
    }
  }
}

runWithRetry().catch((e) => {
  console.error("✗ TCK crashed:", e.message);
  process.exit(1);
});
