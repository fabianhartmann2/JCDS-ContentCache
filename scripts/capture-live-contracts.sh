#!/usr/bin/env bash
set -euo pipefail

umask 077

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="${repository_root}/deploy/contract-capture/compose.yaml"
project_name="jcds-contract-capture-$$"
report_file="$(mktemp "${TMPDIR:-/tmp}/jcds-contract-report.XXXXXX")"

compose() {
  docker compose \
    --env-file /dev/null \
    --project-name "${project_name}" \
    --file "${compose_file}" \
    "$@"
}

cleanup() {
  compose down --remove-orphans >/dev/null 2>&1 || true
  rm -f -- "${report_file}"
}
trap cleanup EXIT

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "Docker is required but was not found." >&2
    exit 1
  fi
  if ! docker compose version >/dev/null 2>&1; then
    echo "The Docker Compose plugin is required but was not found." >&2
    exit 1
  fi
}

prompt_value() {
  local variable_name="$1"
  local prompt_text="$2"
  local secret="${3:-false}"
  local current_value="${!variable_name:-}"

  if [[ -n "${current_value}" ]]; then
    export "${variable_name}=${current_value}"
    return
  fi
  if [[ ! -t 0 ]]; then
    echo "${variable_name} must be exported when running non-interactively." >&2
    exit 1
  fi

  if [[ "${secret}" == "true" ]]; then
    read -r -s -p "${prompt_text}: " current_value
    echo >&2
  else
    read -r -p "${prompt_text}: " current_value
  fi
  if [[ -z "${current_value}" ]]; then
    echo "${variable_name} must not be empty." >&2
    exit 1
  fi
  export "${variable_name}=${current_value}"
}

require_docker
prompt_value JAMF_BASE_URL "Jamf Pro base URL (https://tenant.example)"
prompt_value JAMF_CLIENT_ID "Read-only Jamf API client ID"
prompt_value JAMF_CLIENT_SECRET "Read-only Jamf API client secret" true
prompt_value CAPTURE_PACKAGE_NAME "Existing flat .pkg filename"

JAMF_BASE_URL="${JAMF_BASE_URL%/}"
if [[ "${JAMF_BASE_URL}" != https://* ]]; then
  echo "JAMF_BASE_URL must use HTTPS." >&2
  exit 1
fi
export JAMF_TOKEN_URL="${JAMF_BASE_URL}/api/v1/oauth/token"
export JAMF_CATALOG_URL="${JAMF_BASE_URL}/api/v1/jcds/files"
export JAMF_RESOLVER_URL_TEMPLATE="${JAMF_BASE_URL}/api/v1/jcds/files/{filename}"

echo "Building the credential-free capture image..." >&2
compose build contract-capture >&2

echo "Running sanitized catalog, resolver, HEAD, and one-byte range probes..." >&2
compose run --rm --no-deps -T contract-capture >"${report_file}"

for sensitive_value in "${JAMF_BASE_URL}" "${JAMF_CLIENT_ID}" "${JAMF_CLIENT_SECRET}" "${CAPTURE_PACKAGE_NAME}"; do
  if [[ ${#sensitive_value} -ge 4 ]] && grep --fixed-strings --quiet -- "${sensitive_value}" "${report_file}"; then
    echo "The disclosure guard rejected the generated report." >&2
    exit 1
  fi
done

sed -n '1,$p' "${report_file}"
