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

assert_request_id_header() {
  local header_file="$1"
  if ! sed 's/\r$//' "${header_file}" \
    | grep --extended-regexp --ignore-case --quiet '^X-Request-ID: [0-9a-f]{32}$'; then
    echo "Expected a 32-character hexadecimal X-Request-ID header" >&2
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
assert_request_id_header "${temporary_directory}/miss.headers"
cmp "${fixture}" "${temporary_directory}/miss.pkg"

metrics_after_miss="$(mock_metrics)"
assert_first_fill_metrics "${metrics_after_miss}"

curl --fail --silent --show-error \
  --dump-header "${temporary_directory}/local.headers" \
  --output "${temporary_directory}/local.pkg" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/local.headers" LOCAL
assert_request_id_header "${temporary_directory}/local.headers"
cmp "${fixture}" "${temporary_directory}/local.pkg"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

curl --fail --silent --show-error \
  --head \
  --dump-header "${temporary_directory}/head.headers" \
  --output /dev/null \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/head.headers" LOCAL
assert_request_id_header "${temporary_directory}/head.headers"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

curl --fail --silent --show-error \
  --range 0-0 \
  --dump-header "${temporary_directory}/zero-range.headers" \
  --output "${temporary_directory}/zero-range.body" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/zero-range.headers" LOCAL
assert_request_id_header "${temporary_directory}/zero-range.headers"
grep --fixed-strings --ignore-case --quiet "HTTP/1.1 206 Partial Content" "${temporary_directory}/zero-range.headers"
cmp <(dd if="${fixture}" bs=1 count=1 status=none) "${temporary_directory}/zero-range.body"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

curl --fail --silent --show-error \
  --range 5-9 \
  --dump-header "${temporary_directory}/range.headers" \
  --output "${temporary_directory}/range.body" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/range.headers" LOCAL
assert_request_id_header "${temporary_directory}/range.headers"
grep --fixed-strings --ignore-case --quiet "HTTP/1.1 206 Partial Content" "${temporary_directory}/range.headers"
cmp <(dd if="${fixture}" bs=1 skip=5 count=5 status=none) "${temporary_directory}/range.body"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

curl --fail --silent --show-error \
  --header 'Range: bytes=0-0,5-5' \
  --dump-header "${temporary_directory}/multi-range.headers" \
  --output "${temporary_directory}/multi-range.body" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/multi-range.headers" LOCAL
assert_request_id_header "${temporary_directory}/multi-range.headers"
grep --fixed-strings --ignore-case --quiet "HTTP/1.1 206 Partial Content" "${temporary_directory}/multi-range.headers"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

compose restart cache-helper nginx
wait_until_ready

curl --fail --silent --show-error \
  --dump-header "${temporary_directory}/restart.headers" \
  --output "${temporary_directory}/restart.pkg" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/restart.headers" LOCAL
assert_request_id_header "${temporary_directory}/restart.headers"
cmp "${fixture}" "${temporary_directory}/restart.pkg"
[[ "$(mock_metrics)" == "${metrics_after_miss}" ]]

compose stop mock-upstream

curl --fail --silent --show-error \
  --dump-header "${temporary_directory}/outage-local.headers" \
  --output "${temporary_directory}/outage-local.pkg" \
  http://127.0.0.1:8443/packages/ExampleFile.pkg
assert_source_header "${temporary_directory}/outage-local.headers" LOCAL
assert_request_id_header "${temporary_directory}/outage-local.headers"
cmp "${fixture}" "${temporary_directory}/outage-local.pkg"

outage_status="$(curl --silent --show-error \
  --output "${temporary_directory}/outage-miss.body" \
  --write-out '%{http_code}' \
  http://127.0.0.1:8443/packages/Missing.pkg)"
[[ "${outage_status}" == "502" ]]
grep --fixed-strings --line-regexp --quiet "package source is unavailable" "${temporary_directory}/outage-miss.body"
compose exec --no-TTY cache-helper test ! -e /srv/jamf-store/packages/Missing.pkg

compose logs --no-color nginx >"${temporary_directory}/nginx.log"
python3 - "${temporary_directory}/nginx.log" <<'PY'
import json
import re
import sys

log_path = sys.argv[1]
raw_log = open(log_path, encoding="utf-8").read()

for forbidden in (
    "ExampleFile.pkg",
    "Missing.pkg",
    "/packages/",
    "bytes=",
    "curl/",
):
    if forbidden in raw_log:
        raise SystemExit(f"sensitive request detail leaked into NGINX log: {forbidden!r}")

records = []
marker = '{"event":"package_request"'
for line in raw_log.splitlines():
    start = line.find(marker)
    if start < 0:
        continue
    records.append(json.loads(line[start:]))

if not records:
    raise SystemExit("no package_request behavior records found in NGINX logs")

expected_keys = {
    "event",
    "timestamp",
    "client",
    "client_kind",
    "request_id",
    "connection",
    "connection_requests",
    "http_protocol",
    "method",
    "range_kind",
    "if_range",
    "status",
    "source",
    "response_range",
    "response_length",
    "bytes_sent",
    "request_seconds",
    "upstream_status",
    "upstream_seconds",
    "completion",
}

for record in records:
    if set(record) != expected_keys:
        raise SystemExit(f"unexpected behavior-log schema: {sorted(record)}")
    if record["event"] != "package_request":
        raise SystemExit(f"unexpected event: {record!r}")
    if record["client_kind"] != "curl":
        raise SystemExit(f"unexpected sanitized client class: {record!r}")
    if not record["client"]:
        raise SystemExit(f"missing client correlation address: {record!r}")
    if not re.fullmatch(r"[0-9a-f]{32}", record["request_id"]):
        raise SystemExit(f"invalid request ID: {record!r}")

def require_record(**expected):
    for record in records:
        if all(record.get(key) == value for key, value in expected.items()):
            return
    raise SystemExit(f"missing behavior record {expected!r}; records={records!r}")

require_record(method="GET", range_kind="none", status=200, source="JCDS", completion="complete")
require_record(method="GET", range_kind="none", status=200, source="LOCAL", completion="complete")
require_record(method="HEAD", range_kind="none", status=200, source="LOCAL", completion="complete")
require_record(method="GET", range_kind="start_zero", status=206, source="LOCAL", response_range="present")
require_record(method="GET", range_kind="resume", status=206, source="LOCAL", response_range="present")
require_record(method="GET", range_kind="multi", status=206, source="LOCAL", response_range="present")
require_record(method="GET", range_kind="none", status=502, source="", completion="complete")
PY

echo "Compose smoke test passed: one upstream fill, local range/restart persistence, outage behavior, and privacy-safe NGINX monitoring."
