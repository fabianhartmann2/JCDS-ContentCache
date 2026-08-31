#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
compose_file="${repository_root}/deploy/macos-production/compose.yaml"
helper_environment="${repository_root}/deploy/macos-production/cache-helper.env.example"
temporary_directory="$(mktemp -d)"

cleanup() {
  docker compose --file "${compose_file}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  if [[ -n "${temporary_directory}" && -d "${temporary_directory}" ]]; then
    rm -rf -- "${temporary_directory}"
  fi
}
trap cleanup EXIT

tls_directory="${temporary_directory}/tls"
mkdir -p "${tls_directory}"

openssl req \
  -x509 \
  -newkey rsa:2048 \
  -nodes \
  -days 1 \
  -subj '/CN=jcds-cache.appfruit.ch' \
  -keyout "${tls_directory}/privkey.pem" \
  -out "${tls_directory}/fullchain.pem" \
  >/dev/null 2>&1
chmod 0600 "${tls_directory}/privkey.pem"

export JCDS_MAC_PROD_HELPER_ENV_FILE="${helper_environment}"
export JCDS_MAC_PROD_TLS_DIR="${tls_directory}"
export JCDS_MAC_PROD_LISTEN_IP=127.0.0.1
export JCDS_MAC_PROD_LISTEN_PORT=18443
export JCDS_MAC_PROD_IMAGE_TAG=ci

docker compose --file "${compose_file}" config --quiet

if grep -q 'allow 192\.168\.0\.0/16' "${repository_root}/deploy/macos-production/nginx.conf"; then
  echo "The macOS production profile must not claim ineffective source-CIDR enforcement behind Docker Desktop" >&2
  exit 1
fi

docker compose --file "${compose_file}" build nginx cache-helper
docker compose --file "${compose_file}" run --rm --no-deps store-init

docker compose --file "${compose_file}" run --rm --no-deps \
  --entrypoint /bin/sh cache-helper -eu -c '
    test "$(id -u)" -eq 65532
    test "$(id -g)" -eq 0
    temporary=/srv/jamf-store/.temporary/production-ci-write
    final=/srv/jamf-store/packages/production-ci-write
    : >"${temporary}"
    mv "${temporary}" "${final}"
    test -f "${final}"
    rm "${final}"
  '

# Start the actual non-root helper so its Store.New path proves that a
# pre-provisioned, group-writable Docker volume is accepted even when chmod is
# rejected for the non-owner UID.
docker compose --file "${compose_file}" up --detach cache-helper
for _ in $(seq 1 30); do
  helper_health="$(
    docker compose --file "${compose_file}" ps \
      --format json cache-helper \
      | grep -o '"Health":"[^"]*"' \
      | head -n 1 \
      | cut -d '"' -f 4 \
      || true
  )"
  if [[ "${helper_health}" == "healthy" ]]; then
    break
  fi
  sleep 1
done
if [[ "${helper_health:-}" != "healthy" ]]; then
  docker compose --file "${compose_file}" logs --no-color cache-helper >&2
  echo "Production helper did not become healthy" >&2
  exit 1
fi

docker run --rm \
  --read-only \
  --cap-drop ALL \
  --cap-add CHOWN \
  --cap-add DAC_READ_SEARCH \
  --cap-add SETGID \
  --cap-add SETUID \
  --add-host cache-helper:127.0.0.1 \
  --mount "type=bind,source=${tls_directory},target=/etc/nginx/tls,readonly" \
  --tmpfs /var/cache/nginx:rw,noexec,nosuid,size=64m \
  --tmpfs /var/run:rw,noexec,nosuid,size=8m \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  "jcds-content-cache-nginx:${JCDS_MAC_PROD_IMAGE_TAG}" \
  nginx -t

echo "macOS production profile smoke test passed: Compose, non-root volume writes, baked NGINX, and TLS paths are valid."
