#!/bin/sh
set -eu

system_ca_bundle="/etc/ssl/certs/ca-certificates.crt"
custom_ca_file="${CAPTURE_CA_CERT_FILE:-}"

if [ -n "${custom_ca_file}" ]; then
  if [ ! -r "${custom_ca_file}" ]; then
    echo "The mounted enterprise CA file is not readable." >&2
    exit 1
  fi
  if ! grep -q -- "-----BEGIN CERTIFICATE-----" "${custom_ca_file}"; then
    echo "The mounted enterprise CA file must contain PEM certificates." >&2
    exit 1
  fi

  combined_ca_bundle="/tmp/capture-ca-bundle.pem"
  umask 077
  cat "${system_ca_bundle}" "${custom_ca_file}" >"${combined_ca_bundle}"
  export SSL_CERT_FILE="${combined_ca_bundle}"
fi

exec "$@"
