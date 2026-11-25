#!/usr/bin/env bash
# Run tests only for packages touched by uncommitted changes and their
# dependents. Core-wide changes (go.mod, IR, specmeta, bundled bridges) fall
# back to the full suite. Logic lives in this file; Makefile only calls it.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PY=""
for candidate in python3 python; do
  # Windows installs a Microsoft Store stub named python3 that exits 49.
  if command -v "$candidate" >/dev/null 2>&1 && "$candidate" -c 'import sys' >/dev/null 2>&1; then
    PY="$candidate"
    break
  fi
done
[[ -n "$PY" ]] || { echo "error: a working python3 is required" >&2; exit 1; }
cd "$ROOT"

changed="$(git diff --name-only HEAD -- '*.go' go.mod go.sum bridges/ || true)"
if [[ -z "$changed" ]]; then
  echo "test-impact: no changed Go files; running the full suite"
  exec go test -count=1 ./...
fi

packages="$("$PY" - "$changed" <<'PY'
import json, os, subprocess, sys
from collections import deque, defaultdict

changed = [p.strip().replace("\\", "/") for p in sys.argv[1].splitlines() if p.strip()]
core = ("go.mod", "go.sum", "bridges/", "bridges_embed.go",
        "internal/specmeta/", "internal/ir/", "internal/renderer/")
if any(p == c or p.startswith(c) for p in changed for c in core if c.endswith("/")) or \
   any(p == c for p in changed for c in core if not c.endswith("/")):
    print("ALL")
    sys.exit(0)

raw = subprocess.check_output(["go", "list", "-json", "./..."], text=True)
dec = json.JSONDecoder()
pkgs, i = [], 0
while i < len(raw):
    while i < len(raw) and raw[i].isspace():
        i += 1
    if i >= len(raw):
        break
    obj, i = dec.raw_decode(raw, i)
    pkgs.append(obj)

by_dir = {os.path.abspath(p["Dir"]): p["ImportPath"] for p in pkgs}
roots = set()
for path in changed:
    if not path.endswith(".go"):
        continue
    d = os.path.abspath(os.path.dirname(path))
    if d in by_dir:
        roots.add(by_dir[d])

reverse = defaultdict(set)
for p in pkgs:
    for dep in set(p.get("Deps", [])) | set(p.get("Imports", [])) | \
                set(p.get("TestImports", [])) | set(p.get("XTestImports", [])):
        reverse[dep].add(p["ImportPath"])

seen, q = set(roots), deque(roots)
# A change confined to _test.go files cannot affect other packages.
test_only = all(p.endswith("_test.go") for p in changed if p.endswith(".go"))
if not test_only:
    while q:
        for dependent in reverse[q.popleft()]:
            if dependent not in seen:
                seen.add(dependent)
                q.append(dependent)

if not seen:
    print("ALL")
else:
    print(" ".join(sorted(seen)))
PY
)"

if [[ "$packages" == "ALL" ]]; then
  echo "test-impact: core-wide change; running the full suite"
  exec go test -count=1 ./...
fi

echo "test-impact: $packages"
# shellcheck disable=SC2086
exec go test -count=1 $packages
