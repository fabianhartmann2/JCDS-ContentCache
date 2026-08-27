#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
compose_file="${repository_root}/deploy/production/compose.yaml"
nginx_config="${repository_root}/deploy/nginx/nginx.production.conf.template"
helper_environment="${repository_root}/deploy/production/cache-helper.env.example"
temporary_directory="$(mktemp -d)"

cleanup() {
  if [[ -n "${temporary_directory}" && -d "${temporary_directory}" ]]; then
    rm -rf -- "${temporary_directory}"
  fi
}
trap cleanup EXIT

certificate_directory="${temporary_directory}/letsencrypt/live/jcds-cache.appfruit.ch"
store_directory="${temporary_directory}/jamf-store"
mkdir -p "${certificate_directory}" "${store_directory}/packages" "${store_directory}/.temporary"

openssl req \
  -x509 \
  -newkey rsa:2048 \
  -nodes \
  -days 1 \
  -subj '/CN=jcds-cache.appfruit.ch' \
  -keyout "${certificate_directory}/privkey.pem" \
  -out "${certificate_directory}/fullchain.pem" \
  >/dev/null 2>&1

CACHE_LISTEN_IP=127.0.0.1 \
PACKAGE_STORE_PATH="${store_directory}" \
CACHE_HELPER_ENV_FILE="${helper_environment}" \
LETSENCRYPT_ROOT="${temporary_directory}/letsencrypt" \
  docker compose --file "${compose_file}" config --quiet

docker run --rm \
  --read-only \
  --cap-drop ALL \
  --cap-add CHOWN \
  --cap-add SETGID \
  --cap-add SETUID \
  --add-host cache-helper:127.0.0.1 \
  --mount "type=bind,source=${nginx_config},target=/etc/nginx/nginx.conf,readonly" \
  --mount "type=bind,source=${temporary_directory}/letsencrypt,target=/etc/letsencrypt,readonly" \
  --mount "type=bind,source=${store_directory},target=/srv/jamf-store,readonly" \
  --tmpfs /var/cache/nginx:rw,noexec,nosuid,size=64m \
  --tmpfs /var/run:rw,noexec,nosuid,size=8m \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  nginx:1.30.4-alpine \
  nginx -t

CERTIFICATE_WARNING_DAYS=0 \
  "${repository_root}/scripts/check-certificate-expiry.sh" "${certificate_directory}/fullchain.pem" \
  >/dev/null

if CERTIFICATE_WARNING_DAYS=30 \
  "${repository_root}/scripts/check-certificate-expiry.sh" "${certificate_directory}/fullchain.pem" \
  >/dev/null 2>&1; then
  echo "Expected the one-day synthetic certificate to trigger the 30-day warning" >&2
  exit 1
fi

echo "Production configuration smoke test passed: Compose, hardened NGINX, TLS paths, and expiry checks are valid."
