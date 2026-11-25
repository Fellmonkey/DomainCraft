#!/usr/bin/env bash
# Single TCK entrypoint. The workflow only declares jobs; all logic is here.
# Usage: tck.sh <discover|node|backend-up|backend-down|interop|logs> [target-json]
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOMAIN=compliance-suite/kitchen-sink.yaml
PY="$(command -v python3 || command -v python || true)"
cd "$ROOT"

fail() { echo "error: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "tool not found: $1"; }
[[ -n "$PY" ]] || fail "python3 not found"

field() {
  "$PY" - "$1" "$2" "${3-}" <<'PY'
import json, sys
v = json.loads(sys.argv[1]).get(sys.argv[2])
print(sys.argv[3] if v is None else json.dumps(v, separators=(",", ":")) if isinstance(v, (dict, list)) else v)
PY
}

output_dir() {
  local out; out="$(field "$1" output)"
  if [[ -n "$out" ]]; then echo "$out"; else out="$(field "$1" tck_dir)"; [[ -n "$out" ]] || return 1; echo "$out/generated"; fi
}

generate() {
  local target="$1"
  need go
  local bridge out
  bridge="$(field "$target" bridge)"
  out="$(output_dir "$target")"
  echo "generate: $bridge -> $out" >&2
  go run ./cmd/domaincraft generate --domain "$DOMAIN" --bridge "$bridge" --output "$out" --non-interactive
}

# Scope in one pass: core change or force -> all; bridge change -> that bridge
# plus every target whose extends chain contains it. No jq, no Go command.
discover() {
  local changed="" force=0
  if [[ "${TCK_SCOPE_FORCE:-0}" == 1 ]]; then force=1
  elif [[ -n "${GITHUB_BASE_REF:-}" ]]; then
    git fetch origin "$GITHUB_BASE_REF" || fail "cannot fetch $GITHUB_BASE_REF"
    changed="$(git diff --name-only "origin/$GITHUB_BASE_REF...HEAD")"
  elif [[ ! -t 0 ]]; then changed="$(cat)"; fi
  echo "scope: $force force, $(printf '%s\n' "$changed" | awk 'NF{n++}END{print n+0}') changed paths" >&2

  local result
  result="$("$PY" - "$changed" "$force" <<'PY'
import json, os, pathlib, sys
matrix = json.load(open("compliance-suite/tck-matrix.json", encoding="utf-8"))
raw = matrix.get("targets", [])
ids = [t.get("id", "") for t in raw]
if not ids or len(ids) != len(set(ids)):
    sys.exit("tck-matrix.json: targets must be non-empty with unique ids")
kinds = {"hermetic", "glue", "k6"}
for t in raw:
    k, tid = t.get("kind"), t["id"]
    if k not in kinds: sys.exit("%s: unknown kind %r" % (tid, k))
    if not t.get("bridge"): sys.exit("%s: missing bridge" % tid)
    if k == "hermetic" and not (t.get("tck_dir") and t.get("script")): sys.exit("%s: hermetic needs tck_dir and script" % tid)
    if k == "glue" and not (t.get("output") and t.get("script")): sys.exit("%s: glue needs output and script" % tid)
    if k == "k6" and not (t.get("output") and t.get("port") and t.get("health_path")): sys.exit("%s: k6 needs output, port, health_path" % tid)
    for it in t.get("interop", []):
        if not (it.get("id") and it.get("bridge") and it.get("tck_dir") and it.get("script")):
            sys.exit("%s: interop needs id, bridge, tck_dir, script" % tid)
targets = [t for t in raw if t.get("enabled", True)]
changed = [p.strip().replace("\\", "/") for p in sys.argv[1].splitlines() if p.strip()]
force = sys.argv[2] == "1"
core = ("internal/", "pkg/", "spec/", "cmd/", "compliance-suite/", ".github/workflows/", "go.mod", "go.sum")

def norm(p): return os.path.normpath(p).replace("\\", "/")

if force or not changed or any(p.startswith(core) for p in changed):
    selected = targets
else:
    manifests = list(pathlib.Path("bridges").rglob("bridge.yaml"))
    graph, registry = {}, {}
    for m in manifests:
        d = norm(str(m.parent)); ext = name = ""
        for line in m.read_text(encoding="utf-8").splitlines():
            key, _, value = line.partition(":")
            if key.strip() == "extends": ext = value.strip().strip("\"'")
            if key.strip() == "name": name = value.strip().strip("\"'")
        graph[d] = ext
        if name: registry[name] = d

    def resolve(ref, base):
        if not ref: return ""
        if ref in registry: return registry[ref]
        if ref.startswith("."): return norm(os.path.join(os.path.dirname(base), ref))
        for cand in (ref, norm(os.path.join(os.path.dirname(base), ref))):
            if os.path.isfile(os.path.join(cand, "bridge.yaml")): return norm(cand)
        sys.exit("unresolved extends ref %r in %s: use a registry id or local path" % (ref, base))

    def chain(start):
        out, seen = [], set()
        while start and start not in seen:
            seen.add(start); out.append(start)
            start = resolve(graph.get(start, ""), start)
        return out

    roots = set()
    for p in changed:
        cur = norm(p)
        while cur and cur != ".":
            if os.path.isfile(os.path.join(cur, "bridge.yaml")):
                roots.add(cur); break
            cur = os.path.dirname(cur)
    selected = [t for t in targets if any(r in chain(norm(t["bridge"])) for r in roots)]
    if roots and not selected:
        sys.exit("bridge change selected no TCK target: add the bridge to tck-matrix.json")

# An interop oracle lives in a client TCK but runs inside the k6 job. A change
# to it must select the backend target that executes it, not just the client.
ids = {t["id"] for t in selected}
for t in targets:
    for item in t.get("interop", []):
        d = item.get("tck_dir", "").rstrip("/")
        if d and any(p == d or p.startswith(d + "/") for p in changed):
            ids.add(t["id"])
selected = [t for t in targets if t["id"] in ids]

def bucket(kinds):
    return json.dumps([t for t in selected if t["kind"] in kinds], separators=(",", ":"))
print("node=" + bucket(("hermetic", "glue")))
print("k6=" + bucket(("k6",)))
PY
)"
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    echo "$result" >> "$GITHUB_OUTPUT"
    echo "scope: written to GITHUB_OUTPUT" >&2
  else
    echo "$result"
  fi
}

node_suite() {
  local target="$1" kind dir script out
  kind="$(field "$target" kind)"
  generate "$target"
  if [[ "$kind" == glue ]]; then
    out="$(output_dir "$target")"; script="$(field "$target" script)"
    echo "typecheck: $script in $out" >&2
    (cd "$out" && npm install --no-audit --no-fund && npm run "$script")
  else
    dir="$(field "$target" tck_dir)"; script="$(field "$target" script)"
    echo "certify: $script in $dir" >&2
    (cd "$dir" && npm ci --no-audit --no-fund && npm run "$script")
  fi
}

backend_up() {
  local target="$1" out url
  generate "$target"
  out="$(output_dir "$target")"
  url="http://localhost:$(field "$target" port 9000)$(field "$target" health_path /health/ready)"
  echo "backend-up: $out, waiting for $url" >&2
  (
    cd "$out"
    docker compose up -d --build
    timeout 180 bash -c 'until [[ "$(curl -s -o /dev/null -w "%{http_code}" "$1")" == "200" ]]; do sleep 2; done' _ "$url" || {
      echo "::error::API did not become healthy" >&2; docker compose logs --tail=100 || true; exit 1; }
  )
}

backend_down() {
  local target="$1" out
  out="$(output_dir "$target")"
  [[ -d "$out" ]] || { echo "backend-down: nothing to stop" >&2; return 0; }
  (cd "$out" && docker compose down --volumes --remove-orphans)
}

interop() {
  local target="$1" port; port="$(field "$target" port 9000)"
  need go; need npm
  "$PY" - "$target" <<'PY' | while read -r item; do
import json, sys
for x in json.loads(sys.argv[1]).get("interop", []):
    print(json.dumps(x, separators=(",", ":")))
PY
    local dir script bridge
    dir="$(field "$item" tck_dir)"; script="$(field "$item" script)"; bridge="$(field "$item" bridge)"
    echo "interop: $script in $dir" >&2
    go run ./cmd/domaincraft generate --domain "$DOMAIN" --bridge "$bridge" --output "$dir/generated" --non-interactive
    (cd "$dir" && npm ci --no-audit --no-fund && API_URL="http://localhost:${port}" npm run "$script")
    echo "::notice::interop $(field "$item" id) passed"
  done
}

logs() {
  local target="$1" out id; out="$(output_dir "$target")"; id="$(field "$target" id)"
  [[ -d "$out" ]] || return 0
  (cd "$out" && docker compose logs --tail=300 > "${RUNNER_TEMP:-$TMPDIR}/compose-${id}.log" || true)
}

case "${1:-}" in
  discover)     discover ;;
  node)         node_suite "$2" ;;
  backend-up)   need docker; need curl; need timeout; backend_up "$2" ;;
  backend-down) need docker; backend_down "$2" ;;
  interop)      interop "$2" ;;
  logs)         need docker; logs "$2" ;;
  *)            echo "usage: $0 <discover|node|backend-up|backend-down|interop|logs> [target-json]" >&2; exit 2 ;;
esac
