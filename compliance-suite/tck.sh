#!/usr/bin/env bash
# TCK entrypoint. The workflow declares jobs; this script owns every step's
# logic. One target row from tck-matrix.json is passed as a JSON argument.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOMAIN="compliance-suite/kitchen-sink.yaml"
CLIENT_BRIDGE="bridges/typescript/client"

cd "$ROOT"

usage() {
  cat <<'EOF'
Usage: compliance-suite/tck.sh <command> [target-json]

Commands:
  discover              Resolve changed paths into hermetic/glue/k6 outputs
  generate <target>     Generate the kitchen-sink artifact for a target
  certify <target>      npm ci + npm run <script> in tck_dir (hermetic)
  typecheck <target>    generate + npm install + npm run <script> (glue)
  backend-up <target>   generate + docker compose up + health wait (k6)
  backend-down <target> docker compose down --volumes --remove-orphans
  interop <target>      Generate the TS client and run each interop suite

discover writes to $GITHUB_OUTPUT when set, otherwise to stdout. Piped stdin
supplies a manual list of changed paths; TCK_SCOPE_FORCE=1 selects all targets.
EOF
}

fail() {
  echo "error: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"
}

field() {
  local target="$1" expression="$2"
  jq -er "$expression" <<< "$target"
}

target_output() {
  local target="$1"
  field "$target" '.output // ((.tck_dir // "") + "/generated")'
}

require_target() {
  [[ $# -ge 1 ]] || fail "missing target JSON argument"
  need jq
}

cmd_discover() {
  need go
  local base="${GITHUB_BASE_REF:-}"
  local force="${TCK_SCOPE_FORCE:-0}"
  local args=(-format github)
  local changed=""

  if [[ "$force" == "1" ]]; then
    echo "scope: TCK_SCOPE_FORCE=1, selecting all targets" >&2
    args+=(-all)
  elif [[ -n "$base" ]]; then
    echo "scope: pull request against $base" >&2
    git fetch origin "$base" || fail "cannot fetch base ref: $base"
    changed="$(git diff --name-only "origin/$base...HEAD")"
    args+=(-base "$base")
  elif [[ ! -t 0 ]]; then
    echo "scope: reading changed paths from stdin" >&2
    changed="$(cat)"
    args+=(-base manual)
  else
    echo "scope: no base ref, selecting all targets" >&2
    args+=(-all)
  fi

  local count
  count="$(printf '%s\n' "$changed" | awk 'NF { n++ } END { print n + 0 }')"
  echo "scope: $count changed path(s)" >&2

  local output
  output="$(printf '%s\n' "$changed" | go run ./cmd/tck-scope "${args[@]}")"

  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    while IFS= read -r line; do
      [[ -n "$line" ]] && printf '%s\n' "$line" >> "$GITHUB_OUTPUT"
    done <<< "$output"
    echo "scope: discover outputs written to GITHUB_OUTPUT" >&2
  else
    printf '%s\n' "$output"
  fi
}

cmd_generate() {
  require_target "$@"
  need go
  local target="$1" bridge output
  bridge="$(field "$target" '.bridge')" || fail "target has no bridge"
  output="$(target_output "$target")" || fail "target has no output or tck_dir"
  echo "generate: $bridge -> $output" >&2
  go run ./cmd/domaincraft generate \
    --domain "$DOMAIN" \
    --bridge "$bridge" \
    --output "$output" \
    --non-interactive
}

cmd_certify() {
  require_target "$@"
  need npm
  local target="$1" dir script
  dir="$(field "$target" '.tck_dir')" || fail "target has no tck_dir"
  script="$(field "$target" '.script')" || fail "target has no script"
  echo "certify: npm run $script in $dir" >&2
  # API_URL stays unset: interop runs in the k6 job against its own backend.
  (
    cd "$dir"
    npm ci --no-audit --no-fund
    npm run "$script"
  )
}

cmd_typecheck() {
  require_target "$@"
  need npm
  local target="$1" output script
  cmd_generate "$target"
  output="$(target_output "$target")" || fail "target has no output or tck_dir"
  script="$(field "$target" '.script')" || fail "target has no script"
  echo "typecheck: npm run $script in $output" >&2
  (
    cd "$output"
    # Generated package.json has no lockfile by design: npm resolves its
    # pinned ranges for this smoke target.
    npm install --no-audit --no-fund
    npm run "$script"
  )
}

cmd_backend_up() {
  require_target "$@"
  need docker
  need curl
  need timeout
  local target="$1" output port health_path url
  cmd_generate "$target"
  output="$(target_output "$target")" || fail "target has no output or tck_dir"
  port="$(field "$target" '.port // 9000')"
  health_path="$(field "$target" '.health_path // "/health/ready"')"
  url="http://localhost:${port}${health_path}"
  echo "backend-up: docker compose up in $output, waiting for $url" >&2
  (
    cd "$output"
    docker compose up -d --build
    if ! timeout 180 bash -c \
      'until [[ "$(curl -s -o /dev/null -w "%{http_code}" "$1")" == "200" ]]; do sleep 2; done' \
      _ "$url"; then
      echo "error: API did not become healthy in time" >&2
      docker compose logs --tail=100 || true
      exit 1
    fi
  )
}

cmd_backend_down() {
  require_target "$@"
  need docker
  local target="$1" output
  output="$(target_output "$target")" || fail "target has no output or tck_dir"
  if [[ ! -d "$output" ]]; then
    echo "backend-down: $output not present, nothing to stop" >&2
    return 0
  fi
  echo "backend-down: docker compose down in $output" >&2
  (
    cd "$output"
    docker compose down --volumes --remove-orphans
  )
}

cmd_interop() {
  require_target "$@"
  need go
  need npm
  local target="$1" port count=0
  port="$(field "$target" '.port // 9000')"
  while IFS= read -r item; do
    [[ -z "$item" ]] && continue
    local id dir script
    id="$(jq -r '.id' <<< "$item")"
    dir="$(jq -r '.tck_dir' <<< "$item")"
    script="$(jq -r '.script' <<< "$item")"
    echo "interop: generating client for $id in $dir" >&2
    go run ./cmd/domaincraft generate \
      --domain "$DOMAIN" \
      --bridge "$CLIENT_BRIDGE" \
      --output "$dir/generated" \
      --non-interactive
    (
      cd "$dir"
      npm ci --no-audit --no-fund
      API_URL="http://localhost:${port}" npm run "$script"
    )
    echo "::notice::interop $id passed"
    count=$((count + 1))
  done < <(jq -c '.interop[]? // empty' <<< "$target")
  echo "interop: $count client suite(s) passed" >&2
}

main() {
  local command="${1:-}"
  [[ -n "$command" ]] || { usage >&2; exit 2; }
  shift
  case "$command" in
    discover)     cmd_discover "$@" ;;
    generate)     cmd_generate "$@" ;;
    certify)      cmd_certify "$@" ;;
    typecheck)    cmd_typecheck "$@" ;;
    backend-up)   cmd_backend_up "$@" ;;
    backend-down) cmd_backend_down "$@" ;;
    interop)      cmd_interop "$@" ;;
    -h|--help|help) usage ;;
    *) fail "unknown subcommand: $command (try --help)" ;;
  esac
}

main "$@"
