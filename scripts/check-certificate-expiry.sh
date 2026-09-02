#!/usr/bin/env bash
set -euo pipefail

certificate_path="${1:-/etc/letsencrypt/live/jcds-cache.appfruit.ch/fullchain.pem}"
warning_days="${CERTIFICATE_WARNING_DAYS:-30}"

if [[ ! "${warning_days}" =~ ^[0-9]+$ ]]; then
  echo "CERTIFICATE_WARNING_DAYS must be a non-negative integer" >&2
  exit 2
fi
if [[ ! -r "${certificate_path}" ]]; then
  echo "Certificate is not readable: ${certificate_path}" >&2
  exit 2
fi

warning_seconds=$((warning_days * 24 * 60 * 60))
if ! openssl x509 -checkend "${warning_seconds}" -noout -in "${certificate_path}"; then
  echo "Certificate expires within ${warning_days} days: ${certificate_path}" >&2
  openssl x509 -noout -enddate -in "${certificate_path}" >&2
  exit 1
fi

echo "Certificate remains valid for more than ${warning_days} days."
openssl x509 -noout -subject -issuer -enddate -in "${certificate_path}"
