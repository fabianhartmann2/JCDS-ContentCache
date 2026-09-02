#!/usr/bin/env bash
set -euo pipefail

umask 077

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="${repository_root}/deploy/contract-capture/compose.yaml"
project_name="jcds-contract-capture-$$"
report_file="$(mktemp "${TMPDIR:-/tmp}/jcds-contract-report.XXXXXX")"
mounted_ca_file=""

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
  if [[ -n "${mounted_ca_file}" ]]; then
    rm -f -- "${mounted_ca_file}"
  fi
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

prompt_optional_ca_file() {
  local current_value="${CAPTURE_CA_CERT_FILE:-}"

  if [[ -z "${current_value}" && -t 0 ]]; then
    read -r -p "Enterprise root/intermediate CA bundle in PEM format (optional; press Return to skip): " current_value
  fi
  if [[ -z "${current_value}" ]]; then
    return
  fi
  if [[ ! -f "${current_value}" || ! -r "${current_value}" ]]; then
    echo "CAPTURE_CA_CERT_FILE must be a readable regular file." >&2
    exit 1
  fi
  if ! grep --quiet -- "-----BEGIN CERTIFICATE-----" "${current_value}"; then
    echo "CAPTURE_CA_CERT_FILE must contain PEM certificates." >&2
    exit 1
  fi

  local ca_directory
  ca_directory="$(cd "$(dirname "${current_value}")" && pwd -P)"
  export CAPTURE_CA_CERT_FILE="${ca_directory}/$(basename "${current_value}")"
}

require_docker
prompt_value JAMF_BASE_URL "Jamf Pro base URL (https://tenant.example)"
prompt_value JAMF_CLIENT_ID "Read-only Jamf API client ID"
prompt_value JAMF_CLIENT_SECRET "Read-only Jamf API client secret" true
prompt_value CAPTURE_PACKAGE_NAME "Existing flat .pkg filename"
prompt_optional_ca_file

if [[ -n "${CAPTURE_CA_CERT_FILE:-}" ]]; then
  mounted_ca_file="$(mktemp "${TMPDIR:-/tmp}/jcds-capture-ca.XXXXXX")"
  cp "${CAPTURE_CA_CERT_FILE}" "${mounted_ca_file}"
  chmod 0444 "${mounted_ca_file}"
fi

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
run_options=(run --rm --no-deps -T)
if [[ -n "${CAPTURE_CA_CERT_FILE:-}" ]]; then
  run_options+=(
    --env CAPTURE_CA_CERT_FILE=/run/secrets/enterprise-ca.pem
    --volume "${mounted_ca_file}:/run/secrets/enterprise-ca.pem:ro"
  )
fi
run_options+=(contract-capture)
compose "${run_options[@]}" >"${report_file}"

for sensitive_value in "${JAMF_BASE_URL}" "${JAMF_CLIENT_ID}" "${JAMF_CLIENT_SECRET}" "${CAPTURE_PACKAGE_NAME}"; do
  if [[ ${#sensitive_value} -ge 4 ]] && grep --fixed-strings --quiet -- "${sensitive_value}" "${report_file}"; then
    echo "The disclosure guard rejected the generated report." >&2
    exit 1
  fi
done

sed -n '1,$p' "${report_file}"
