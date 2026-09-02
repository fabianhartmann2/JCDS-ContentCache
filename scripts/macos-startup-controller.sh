#!/bin/bash

set -u
set -o pipefail

umask 077

program_name="$(basename "$0")"
repository_root=""
runtime_root=""
health_url=""
ca_file=""
with_monitoring="false"
docker_ready_timeout=300
container_ready_timeout=300
https_ready_timeout=120
poll_interval=5
base_compose_sha256=""
monitoring_compose_sha256=""
validate_only="false"
config_file=""
started_epoch="$(date +%s)"
operational_log=""
current_status_file=""
lock_directory=""
lock_acquired="false"

usage() {
  cat <<'EOF'
Usage:
  macos-startup-controller.sh --config FILE [--validate-only]
  macos-startup-controller.sh --repository-root PATH --runtime-root PATH \
    --health-url HTTPS_URL --base-compose-sha256 SHA256 [options]

Options:
  --with-monitoring                 Include compose.monitoring.yaml.
  --without-monitoring              Use only the base production Compose file.
  --monitoring-compose-sha256 HASH  Required with --with-monitoring.
  --ca-file PATH                    Explicit CA bundle for the HTTPS readiness probe.
  --docker-ready-timeout SECONDS    Default: 300.
  --container-ready-timeout SECONDS Default: 300.
  --https-ready-timeout SECONDS     Default: 120.
  --poll-interval SECONDS           Default: 5.
  --validate-only                   Validate inputs without starting anything.
EOF
}

configuration_error() {
  local timestamp event temporary_status

  printf '%s: %s\n' "${program_name}" "$1" >&2
  if [[ -n "${operational_log}" && -n "${current_status_file}" ]]; then
    timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    event="{\"timestamp\":\"${timestamp}\",\"event\":\"startup_reconcile\",\"phase\":\"configuration\",\"status\":\"failure\",\"elapsed_seconds\":$(( $(date +%s) - started_epoch )),\"exit_code\":64}"
    printf '%s\n' "${event}" >>"${operational_log}" 2>/dev/null || true
    /bin/chmod 0600 "${operational_log}" 2>/dev/null || true
    temporary_status="${current_status_file}.tmp.$$"
    if printf '%s\n' "${event}" >"${temporary_status}" 2>/dev/null; then
      /bin/chmod 0600 "${temporary_status}" 2>/dev/null || true
      /bin/mv -f "${temporary_status}" "${current_status_file}" 2>/dev/null || true
    fi
  fi
  exit 64
}

valid_token() {
  [[ "$1" =~ ^[a-z0-9_]+$ ]]
}

valid_positive_integer() {
  [[ "$1" =~ ^[1-9][0-9]*$ ]]
}

valid_sha256() {
  [[ "$1" =~ ^[0-9a-f]{64}$ ]]
}

load_config() {
  local file="$1"
  local line key value

  [[ -f "${file}" ]] || configuration_error "configuration file does not exist"

  while IFS= read -r line || [[ -n "${line}" ]]; do
    [[ -z "${line}" || "${line}" == \#* ]] && continue
    [[ "${line}" == *=* ]] || configuration_error "invalid configuration line"
    key="${line%%=*}"
    value="${line#*=}"

    case "${key}" in
      repository_root) repository_root="${value}" ;;
      health_url) health_url="${value}" ;;
      ca_file) ca_file="${value}" ;;
      with_monitoring) with_monitoring="${value}" ;;
      docker_ready_timeout) docker_ready_timeout="${value}" ;;
      container_ready_timeout) container_ready_timeout="${value}" ;;
      https_ready_timeout) https_ready_timeout="${value}" ;;
      poll_interval) poll_interval="${value}" ;;
      base_compose_sha256) base_compose_sha256="${value}" ;;
      monitoring_compose_sha256) monitoring_compose_sha256="${value}" ;;
      *) configuration_error "unknown configuration key: ${key}" ;;
    esac
  done <"${file}"

  runtime_root="$(cd "$(dirname "${file}")" 2>/dev/null && pwd -P)" \
    || configuration_error "cannot resolve runtime directory"
}

# Load the configuration before command-line options so explicit options can
# override non-secret controller settings during an operational test.
arguments=("$@")
argument_index=0
while (( argument_index < ${#arguments[@]} )); do
  if [[ "${arguments[argument_index]}" == "--config" ]]; then
    (( argument_index + 1 < ${#arguments[@]} )) \
      || configuration_error "--config requires a file"
    config_file="${arguments[argument_index + 1]}"
    load_config "${config_file}"
    break
  fi
  argument_index=$((argument_index + 1))
done

while (( $# > 0 )); do
  case "$1" in
    --config)
      (( $# >= 2 )) || configuration_error "--config requires a file"
      shift 2
      ;;
    --repository-root)
      (( $# >= 2 )) || configuration_error "--repository-root requires a path"
      repository_root="$2"
      shift 2
      ;;
    --runtime-root)
      (( $# >= 2 )) || configuration_error "--runtime-root requires a path"
      runtime_root="$2"
      shift 2
      ;;
    --health-url)
      (( $# >= 2 )) || configuration_error "--health-url requires a URL"
      health_url="$2"
      shift 2
      ;;
    --ca-file)
      (( $# >= 2 )) || configuration_error "--ca-file requires a path"
      ca_file="$2"
      shift 2
      ;;
    --with-monitoring)
      with_monitoring="true"
      shift
      ;;
    --without-monitoring)
      with_monitoring="false"
      shift
      ;;
    --docker-ready-timeout)
      (( $# >= 2 )) || configuration_error "--docker-ready-timeout requires seconds"
      docker_ready_timeout="$2"
      shift 2
      ;;
    --container-ready-timeout)
      (( $# >= 2 )) || configuration_error "--container-ready-timeout requires seconds"
      container_ready_timeout="$2"
      shift 2
      ;;
    --https-ready-timeout)
      (( $# >= 2 )) || configuration_error "--https-ready-timeout requires seconds"
      https_ready_timeout="$2"
      shift 2
      ;;
    --poll-interval)
      (( $# >= 2 )) || configuration_error "--poll-interval requires seconds"
      poll_interval="$2"
      shift 2
      ;;
    --base-compose-sha256)
      (( $# >= 2 )) || configuration_error "--base-compose-sha256 requires a hash"
      base_compose_sha256="$2"
      shift 2
      ;;
    --monitoring-compose-sha256)
      (( $# >= 2 )) || configuration_error "--monitoring-compose-sha256 requires a hash"
      monitoring_compose_sha256="$2"
      shift 2
      ;;
    --validate-only)
      validate_only="true"
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      configuration_error "unknown option: $1"
      ;;
  esac
done

[[ "${repository_root}" == /* ]] || configuration_error "repository root must be absolute"
[[ "${runtime_root}" == /* ]] || configuration_error "runtime root must be absolute"
[[ -d "${repository_root}" ]] || configuration_error "repository root does not exist"
[[ -d "${runtime_root}" ]] || configuration_error "runtime root does not exist"

repository_root="$(cd "${repository_root}" && pwd -P)" \
  || configuration_error "cannot resolve repository root"
runtime_root="$(cd "${runtime_root}" && pwd -P)" \
  || configuration_error "cannot resolve runtime root"

/bin/chmod 0700 "${runtime_root}" \
  || configuration_error "cannot protect runtime root"
log_directory="${runtime_root}/logs"
/bin/mkdir -p "${log_directory}" \
  || configuration_error "cannot create protected log directory"
/bin/chmod 0700 "${log_directory}" \
  || configuration_error "cannot protect log directory"
operational_log="${log_directory}/startup-recovery.jsonl"
current_status_file="${runtime_root}/startup-recovery.status"
lock_directory="${runtime_root}/.startup-controller.lock"

case "${health_url}" in
  https://*) ;;
  *) configuration_error "health URL must use HTTPS" ;;
esac
[[ "${health_url}" != *"@"* ]] || configuration_error "health URL must not contain user information"
[[ "${health_url}" != *$'\n'* && "${health_url}" != *$'\r'* && "${health_url}" != *$'\t'* && "${health_url}" != *" "* ]] \
  || configuration_error "health URL contains whitespace"

case "${with_monitoring}" in
  true|false) ;;
  *) configuration_error "with_monitoring must be true or false" ;;
esac

valid_positive_integer "${docker_ready_timeout}" \
  || configuration_error "docker_ready_timeout must be a positive integer"
valid_positive_integer "${container_ready_timeout}" \
  || configuration_error "container_ready_timeout must be a positive integer"
valid_positive_integer "${https_ready_timeout}" \
  || configuration_error "https_ready_timeout must be a positive integer"
valid_positive_integer "${poll_interval}" \
  || configuration_error "poll_interval must be a positive integer"
(( poll_interval <= 60 )) || configuration_error "poll_interval must not exceed 60 seconds"

valid_sha256 "${base_compose_sha256}" \
  || configuration_error "base Compose SHA-256 is missing or invalid"
if [[ "${with_monitoring}" == "true" ]]; then
  valid_sha256 "${monitoring_compose_sha256}" \
    || configuration_error "monitoring Compose SHA-256 is required when monitoring is enabled"
fi

base_compose_file="${repository_root}/deploy/macos-production/compose.yaml"
monitoring_compose_file="${repository_root}/deploy/macos-production/compose.monitoring.yaml"
deployment_environment="${runtime_root}/deployment.production.env"

[[ -f "${base_compose_file}" ]] || configuration_error "base production Compose file is missing"
[[ -f "${deployment_environment}" ]] || configuration_error "private deployment environment is missing"
if [[ "${with_monitoring}" == "true" ]]; then
  [[ -f "${monitoring_compose_file}" ]] \
    || configuration_error "monitoring Compose file is missing"
fi
if [[ -n "${ca_file}" ]]; then
  [[ "${ca_file}" == /* && -r "${ca_file}" ]] \
    || configuration_error "CA file must be an absolute readable file"
fi

file_mode() {
  if [[ "$(uname -s)" == "Darwin" ]]; then
    /usr/bin/stat -f '%Lp' "$1" 2>/dev/null
  else
    /usr/bin/stat -c '%a' "$1" 2>/dev/null
  fi
}

require_private_file() {
  local file="$1"
  local description="$2"
  local mode mode_value

  mode="$(file_mode "${file}")" \
    || configuration_error "cannot inspect permissions for ${description}"
  [[ "${mode}" =~ ^[0-7]{3,4}$ ]] \
    || configuration_error "cannot interpret permissions for ${description}"
  mode_value=$((8#${mode}))
  (( (mode_value & 8#077) == 0 )) \
    || configuration_error "${description} must not be accessible by group or others"
}

require_private_file "${deployment_environment}" "private deployment environment"
if [[ -n "${config_file}" ]]; then
  require_private_file "${config_file}" "startup controller configuration"
fi

sha256_file() {
  if [[ -x /usr/bin/shasum ]]; then
    /usr/bin/shasum -a 256 "$1" | /usr/bin/awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | /usr/bin/awk '{print $1}'
    return
  fi
  configuration_error "no SHA-256 utility is available"
}

actual_base_sha256="$(sha256_file "${base_compose_file}")"
[[ "${actual_base_sha256}" == "${base_compose_sha256}" ]] \
  || configuration_error "base production Compose file differs from the approved installation"
if [[ "${with_monitoring}" == "true" ]]; then
  actual_monitoring_sha256="$(sha256_file "${monitoring_compose_file}")"
  [[ "${actual_monitoring_sha256}" == "${monitoring_compose_sha256}" ]] \
    || configuration_error "monitoring Compose file differs from the approved installation"
fi

rotate_log_if_needed() {
  local size
  [[ -f "${operational_log}" ]] || return 0
  size="$(/usr/bin/wc -c <"${operational_log}" | /usr/bin/tr -d ' ')"
  if [[ "${size}" =~ ^[0-9]+$ ]] && (( size >= 5242880 )); then
    /bin/mv -f "${operational_log}" "${operational_log}.1"
  fi
}

record_event() {
  local phase="$1"
  local status="$2"
  local exit_code="$3"
  local now elapsed timestamp event temporary_status

  valid_token "${phase}" || phase="invalid_phase"
  valid_token "${status}" || status="invalid_status"
  now="$(date +%s)"
  elapsed=$((now - started_epoch))
  timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  event="{\"timestamp\":\"${timestamp}\",\"event\":\"startup_reconcile\",\"phase\":\"${phase}\",\"status\":\"${status}\",\"elapsed_seconds\":${elapsed},\"exit_code\":${exit_code}}"

  rotate_log_if_needed
  printf '%s\n' "${event}" >>"${operational_log}"
  /bin/chmod 0600 "${operational_log}"

  temporary_status="${current_status_file}.tmp.$$"
  printf '%s\n' "${event}" >"${temporary_status}"
  /bin/chmod 0600 "${temporary_status}"
  /bin/mv -f "${temporary_status}" "${current_status_file}"
  printf '%s\n' "${event}"
}

release_lock() {
  if [[ "${lock_acquired}" == "true" ]]; then
    /bin/rm -f "${lock_directory}/pid"
    /bin/rmdir "${lock_directory}" 2>/dev/null || true
    lock_acquired="false"
  fi
}

fail() {
  local phase="$1"
  local exit_code="$2"
  printf '%s: reconciliation failed during %s\n' "${program_name}" "${phase}" >&2
  record_event "${phase}" "failure" "${exit_code}"
  release_lock
  exit "${exit_code}"
}

handle_signal() {
  record_event "controller" "interrupted" 130
  release_lock
  exit 130
}

acquire_lock() {
  local existing_pid=""

  if /bin/mkdir "${lock_directory}" 2>/dev/null; then
    printf '%s\n' "$$" >"${lock_directory}/pid"
    lock_acquired="true"
    return 0
  fi

  if [[ -f "${lock_directory}/pid" ]]; then
    IFS= read -r existing_pid <"${lock_directory}/pid" || true
  fi
  if [[ "${existing_pid}" =~ ^[1-9][0-9]*$ ]] && /bin/kill -0 "${existing_pid}" 2>/dev/null; then
    record_event "controller" "already_running" 0
    exit 0
  fi

  [[ -d "${lock_directory}" && ! -L "${lock_directory}" ]] \
    || fail "lock" 73
  /bin/rm -f "${lock_directory}/pid"
  /bin/rmdir "${lock_directory}" 2>/dev/null || fail "lock" 73
  /bin/mkdir "${lock_directory}" 2>/dev/null || fail "lock" 73
  printf '%s\n' "$$" >"${lock_directory}/pid"
  lock_acquired="true"
}

resolve_executable() {
  local override="$1"
  shift
  local candidate

  if [[ -n "${override}" ]]; then
    [[ "${override}" == /* && -x "${override}" ]] || return 1
    printf '%s\n' "${override}"
    return 0
  fi

  for candidate in "$@"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

docker_binary="$(resolve_executable "${JCDS_STARTUP_DOCKER_BIN:-}" \
  "${HOME:-/nonexistent}/.docker/bin/docker" \
  /usr/local/bin/docker \
  /opt/homebrew/bin/docker \
  /Applications/Docker.app/Contents/Resources/bin/docker)" \
  || configuration_error "Docker CLI was not found"
curl_binary="$(resolve_executable "${JCDS_STARTUP_CURL_BIN:-}" /usr/bin/curl)" \
  || configuration_error "curl was not found"
open_binary="$(resolve_executable "${JCDS_STARTUP_OPEN_BIN:-}" /usr/bin/open)" \
  || configuration_error "open was not found"
sleep_binary="$(resolve_executable "${JCDS_STARTUP_SLEEP_BIN:-}" /bin/sleep)" \
  || configuration_error "sleep was not found"

if [[ "${validate_only}" == "true" ]]; then
  record_event "configuration" "valid" 0
  exit 0
fi

trap handle_signal INT TERM HUP
trap release_lock EXIT
acquire_lock
record_event "controller" "started" 0

docker_ready() {
  "${docker_binary}" info --format '{{.ServerVersion}}' >/dev/null 2>&1
}

if ! docker_ready; then
  if [[ -z "${JCDS_STARTUP_OPEN_BIN:-}" && ! -d /Applications/Docker.app ]]; then
    fail "docker_application" 69
  fi
  record_event "docker_desktop" "starting" 0
  "${open_binary}" -gja /Applications/Docker.app || fail "docker_application" 69
fi

deadline=$(( $(date +%s) + docker_ready_timeout ))
until docker_ready; do
  (( $(date +%s) < deadline )) || fail "docker_engine" 70
  "${sleep_binary}" "${poll_interval}"
done
record_event "docker_engine" "ready" 0

compose_arguments=(
  compose
  --env-file "${deployment_environment}"
  --file "${base_compose_file}"
)
if [[ "${with_monitoring}" == "true" ]]; then
  compose_arguments+=(--file "${monitoring_compose_file}")
fi

"${docker_binary}" "${compose_arguments[@]}" config --quiet \
  || fail "compose_configuration" 78
record_event "compose_configuration" "valid" 0

# Images must be built as part of an approved deployment. Recovery never builds
# source or pulls an unreviewed replacement during boot.
"${docker_binary}" "${compose_arguments[@]}" up --detach --no-build \
  || fail "compose_reconcile" 71
record_event "compose_reconcile" "complete" 0

service_healthy() {
  local service="$1"
  local container_id status

  container_id="$("${docker_binary}" "${compose_arguments[@]}" ps --quiet "${service}" 2>/dev/null \
    | /usr/bin/head -n 1)"
  [[ -n "${container_id}" ]] || return 1
  status="$("${docker_binary}" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${container_id}" 2>/dev/null)"
  [[ "${status}" == "healthy" ]]
}

deadline=$(( $(date +%s) + container_ready_timeout ))
while true; do
  if service_healthy cache-helper \
    && service_healthy cache-maintainer \
    && service_healthy nginx; then
    break
  fi
  (( $(date +%s) < deadline )) || fail "container_health" 72
  "${sleep_binary}" "${poll_interval}"
done
record_event "container_health" "ready" 0

curl_arguments=(
  --fail
  --silent
  --show-error
  --output /dev/null
  --connect-timeout 10
  --max-time 20
  --proto '=https'
)
if [[ -n "${ca_file}" ]]; then
  curl_arguments+=(--cacert "${ca_file}")
fi

https_ready() {
  "${curl_binary}" "${curl_arguments[@]}" "${health_url}" >/dev/null 2>&1
}

deadline=$(( $(date +%s) + https_ready_timeout ))
until https_ready; do
  (( $(date +%s) < deadline )) || fail "https_readiness" 74
  "${sleep_binary}" "${poll_interval}"
done
record_event "https_readiness" "ready" 0
record_event "controller" "success" 0
release_lock
exit 0
