#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
compose_file="${repository_root}/deploy/compose/docker-compose.yml"
fixture="${repository_root}/testdata/packages/ExampleFile.pkg"
project_suffix="${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-$$"
project_name="jcds-cache-smoke-${project_suffix//[^a-zA-Z0-9_-]/-}"
temporary_directory="$(mktemp -d)"

compose() {
  docker compose --project-name "${project_name}" --file "${compose_file}" "$@"
}

cleanup() {
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  if [[ -n "${temporary_directory}" && -d "${temporary_directory}" ]]; then
    rm -rf -- "${temporary_directory}"
  fi
}
trap cleanup EXIT

wait_until_ready() {
  local attempts=60
  while (( attempts > 0 )); do
    if curl --fail --silent --show-error http://127.0.0.1:8443/health/ready >/dev/null 2>&1; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 1
  done
  compose ps
  compose logs --no-color
  return 1
}

assert_source_header() {
  local header_file="$1"
  local expected_source="$2"
  if ! grep --fixed-strings --ignore-case --quiet "X-Package-Source: ${expected_source}" "${header_file}"; then
    echo "Expected X-Package-Source: ${expected_source}" >&2
    sed 's/\r$//' "${header_file}" >&2
    return 1
  fi
}

mock_metrics() {
  compose exec --no-TTY mock-upstream wget -qO- http://127.0.0.1:8081/metrics
}

assert_first_fill_metrics() {
  local metrics="$1"
  python3 - "${metrics}" <<'PY'
import json
import sys

actual = json.loads(sys.argv[1])
expected = {
    "token_requests": 1,
    "catalog_requests": 1,
    "resolve_requests": 1,
    "object_requests": 1,
}
if actual != expected:
    raise SystemExit(f"unexpected first-fill metrics: {actual!r}; expected {expected!r}")
PY
}

compose up --build --detach
wait_until_ready

curl --fail --silent --show-error \
  --dump-header "${temporary_directory}/miss.headers" \
  --output "${temporary_directory}/miss.pkg" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/miss.headers" JCDS
cmp "${fixture}" "${temporary_directory}/miss.pkg"

metrics_after_miss="$(mock_metrics)"
assert_first_fill_metrics "${metrics_after_miss}"

curl --fail --silent --show-error \
  --dump-header "${temporary_directory}/local.headers" \
  --output "${temporary_directory}/local.pkg" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/local.headers" LOCAL
cmp "${fixture}" "${temporary_directory}/local.pkg"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

curl --fail --silent --show-error \
  --range 5-9 \
  --dump-header "${temporary_directory}/range.headers" \
  --output "${temporary_directory}/range.body" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/range.headers" LOCAL
grep --fixed-strings --ignore-case --quiet "HTTP/1.1 206 Partial Content" "${temporary_directory}/range.headers"
cmp <(dd if="${fixture}" bs=1 skip=5 count=5 status=none) "${temporary_directory}/range.body"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

compose restart cache-helper nginx
wait_until_ready

curl --fail --silent --show-error \
  --dump-header "${temporary_directory}/restart.headers" \
  --output "${temporary_directory}/restart.pkg" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/restart.headers" LOCAL
cmp "${fixture}" "${temporary_directory}/restart.pkg"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

compose stop mock-upstream

curl --fail --silent --show-error \
  --dump-header "${temporary_directory}/outage-local.headers" \
  --output "${temporary_directory}/outage-local.pkg" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/outage-local.headers" LOCAL
cmp "${fixture}" "${temporary_directory}/outage-local.pkg"

outage_status="$(curl --silent --show-error \
  --output "${temporary_directory}/outage-miss.body" \
  --write-out '%{http_code}' \
  http://127.0.0.1:8443/packages/Missing.pkg)"
[[ "${outage_status}" == "502" ]]
grep --fixed-strings --line-regexp --quiet "package source is unavailable" "${temporary_directory}/outage-miss.body"
compose exec --no-TTY cache-helper test ! -e /srv/jamf-store/packages/Missing.pkg

echo "Compose smoke test passed: one upstream fill, local range/restart persistence, and local delivery during an upstream outage."
