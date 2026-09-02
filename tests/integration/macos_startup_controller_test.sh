#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
controller="${repository_root}/scripts/macos-startup-controller.sh"
temporary_directory="$(mktemp -d)"

cleanup() {
  rm -rf -- "${temporary_directory}"
}
trap cleanup EXIT

runtime_root="${temporary_directory}/runtime"
fake_bin="${temporary_directory}/bin"
state_directory="${temporary_directory}/state"
mkdir -p "${runtime_root}" "${fake_bin}" "${state_directory}"
chmod 0700 "${runtime_root}"
: >"${runtime_root}/deployment.production.env"
chmod 0600 "${runtime_root}/deployment.production.env"

cat >"${fake_bin}/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_STATE_DIRECTORY}/docker.log"

if [[ "${1:-}" == "info" ]]; then
  if [[ "${TEST_DOCKER_ALWAYS_UNREADY:-false}" == "true" ]]; then
    exit 1
  fi
  count=0
  [[ ! -f "${TEST_STATE_DIRECTORY}/docker-info-count" ]] \
    || count="$(<"${TEST_STATE_DIRECTORY}/docker-info-count")"
  count=$((count + 1))
  printf '%s\n' "${count}" >"${TEST_STATE_DIRECTORY}/docker-info-count"
  (( count >= 3 ))
  exit
fi

if [[ "${1:-}" == "compose" ]]; then
  command=""
  for argument in "$@"; do
    case "${argument}" in
      config|up|ps) command="${argument}" ;;
    esac
  done
  case "${command}" in
    config) exit 0 ;;
    up)
      : >"${TEST_STATE_DIRECTORY}/compose-up"
      exit 0
      ;;
    ps)
      printf 'container-%s\n' "${!#}"
      exit 0
      ;;
  esac
fi

if [[ "${1:-}" == "inspect" ]]; then
  printf 'healthy\n'
  exit 0
fi

exit 1
EOF

cat >"${fake_bin}/open" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_STATE_DIRECTORY}/open.log"
EOF

cat >"${fake_bin}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_STATE_DIRECTORY}/curl.log"
count=0
[[ ! -f "${TEST_STATE_DIRECTORY}/curl-count" ]] \
  || count="$(<"${TEST_STATE_DIRECTORY}/curl-count")"
count=$((count + 1))
printf '%s\n' "${count}" >"${TEST_STATE_DIRECTORY}/curl-count"
(( count >= 2 ))
EOF

cat >"${fake_bin}/sleep" <<'EOF'
#!/usr/bin/env bash
/bin/sleep 0.01
EOF

chmod 0700 "${fake_bin}/docker" "${fake_bin}/open" "${fake_bin}/curl" "${fake_bin}/sleep"

base_sha256="$(shasum -a 256 "${repository_root}/deploy/macos-production/compose.yaml" | awk '{print $1}')"
monitoring_sha256="$(shasum -a 256 "${repository_root}/deploy/macos-production/compose.monitoring.yaml" | awk '{print $1}')"
controller_config="${runtime_root}/startup-controller.conf"
cat >"${controller_config}" <<EOF
repository_root=${repository_root}
health_url=https://jcds-cache.example.invalid:8443/health/ready
ca_file=
with_monitoring=true
docker_ready_timeout=10
container_ready_timeout=10
https_ready_timeout=10
poll_interval=1
base_compose_sha256=${base_sha256}
monitoring_compose_sha256=${monitoring_sha256}
EOF
chmod 0600 "${controller_config}"

export TEST_STATE_DIRECTORY="${state_directory}"
export JCDS_STARTUP_DOCKER_BIN="${fake_bin}/docker"
export JCDS_STARTUP_OPEN_BIN="${fake_bin}/open"
export JCDS_STARTUP_CURL_BIN="${fake_bin}/curl"
export JCDS_STARTUP_SLEEP_BIN="${fake_bin}/sleep"

"${controller}" --config "${controller_config}"

test -f "${state_directory}/compose-up"
grep -q -- '-gja /Applications/Docker.app' "${state_directory}/open.log"
grep -q -- 'compose.monitoring.yaml' "${state_directory}/docker.log"
grep -q '"phase":"docker_engine","status":"ready"' \
  "${runtime_root}/logs/startup-recovery.jsonl"
grep -q '"phase":"container_health","status":"ready"' \
  "${runtime_root}/logs/startup-recovery.jsonl"
grep -q '"phase":"https_readiness","status":"ready"' \
  "${runtime_root}/logs/startup-recovery.jsonl"
grep -q '"phase":"controller","status":"success"' \
  "${runtime_root}/startup-recovery.status"

open_count_before="$(wc -l <"${state_directory}/open.log" | tr -d ' ')"
"${controller}" --config "${controller_config}"
open_count_after="$(wc -l <"${state_directory}/open.log" | tr -d ' ')"
[[ "${open_count_before}" == "${open_count_after}" ]]

export TEST_DOCKER_ALWAYS_UNREADY=true
if "${controller}" \
  --repository-root "${repository_root}" \
  --runtime-root "${runtime_root}" \
  --health-url https://jcds-cache.example.invalid:8443/health/ready \
  --base-compose-sha256 "${base_sha256}" \
  --docker-ready-timeout 1 \
  --container-ready-timeout 10 \
  --https-ready-timeout 10 \
  --poll-interval 1 >/dev/null 2>&1; then
  echo "Controller did not fail when the Docker engine missed its timeout" >&2
  exit 1
fi
unset TEST_DOCKER_ALWAYS_UNREADY
grep -q '"phase":"docker_engine","status":"failure"' \
  "${runtime_root}/startup-recovery.status"

if "${controller}" \
  --repository-root "${repository_root}" \
  --runtime-root "${runtime_root}" \
  --health-url https://jcds-cache.example.invalid:8443/health/ready \
  --base-compose-sha256 "$(printf '0%.0s' {1..64})" \
  --validate-only >/dev/null 2>&1; then
  echo "Controller accepted an unapproved base Compose checksum" >&2
  exit 1
fi
grep -q '"phase":"configuration","status":"failure"' \
  "${runtime_root}/startup-recovery.status"

if grep -q -- '--insecure\|-k' "${controller}"; then
  echo "Controller must never disable HTTPS certificate validation" >&2
  exit 1
fi

echo "macOS startup controller test passed: bounded Docker startup, monitored Compose reconciliation, container health, trusted HTTPS, idempotency, and config pinning are valid."
