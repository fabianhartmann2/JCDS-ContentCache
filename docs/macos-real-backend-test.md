# Real-backend test on macOS with Docker Desktop

## Purpose and safety boundary

This procedure validates the real Jamf OAuth, JCDS catalog, file resolver, signed-object download, streaming, SHA3-512 verification and atomic local publication from a Mac. It deliberately keeps the downstream listener on `127.0.0.1` and uses plain HTTP only between the Mac and local NGINX. All Jamf and JCDS connections require validated HTTPS.

This is a controlled integration test, not the production deployment. It uses a Docker-managed test volume and does not expose the service to the LAN. Do not change the listen address to `0.0.0.0` while this profile lacks downstream TLS and client access control.

The Jamf client secret is injected as a container environment value from a private Mac-local file. It remains inspectable by the Mac user and Docker administrators while the container exists. Do not put the completed file inside the Git checkout, paste it into chat, or copy it into tickets or public logs.

## 1. Prepare the private environment file

From the repository root:

```bash
runtime_root="${HOME}/JCDS-ContentCache-runtime"
mkdir -p "${runtime_root}"

cp deploy/macos/cache-helper.real.env.example \
  "${runtime_root}/cache-helper.real.env"
chmod 0600 "${runtime_root}/cache-helper.real.env"

nano "${runtime_root}/cache-helper.real.env"
```

Replace every `REPLACE` value locally:

- `JAMF_TOKEN_URL`: tenant URL ending in `/api/oauth/token`
- `JAMF_CLIENT_ID`: dedicated read-only API client ID
- `JAMF_CLIENT_SECRET`: its client secret
- `JAMF_CATALOG_URL`: tenant URL ending in `/api/v1/jcds/files`
- `JAMF_RESOLVER_URL_TEMPLATE`: tenant URL ending in `/api/v1/jcds/files/{filename}`
- `JCDS_ALLOWED_HOSTS`: exact hostname from the resolver's `uri`, without scheme, path, port or query

Retain `JCDS_ALLOW_HTTP=false`. Never use a wildcard such as `*.cloudfront.net`. The helper revalidates every redirect, so a legitimate redirect to a different hostname will be rejected until that exact additional hostname is reviewed and added.

Confirm that no placeholders remain without printing the file:

```bash
if grep --quiet REPLACE "${runtime_root}/cache-helper.real.env"; then
  echo "The private environment file still contains REPLACE values" >&2
  exit 1
fi

stat -f '%Sp %Su:%Sg %N' "${runtime_root}/cache-helper.real.env"
```

The permissions should begin with `-rw-------`.

## 2. Validate and start the real-backend profile

Export only the path to the private file, never the secret itself:

```bash
export JCDS_MAC_HELPER_ENV_FILE="${runtime_root}/cache-helper.real.env"
export JCDS_MAC_LISTEN_IP=127.0.0.1
export JCDS_MAC_LISTEN_PORT=8443
```

Validate the resolved Compose model:

```bash
docker compose \
  --file deploy/macos/compose.yaml \
  config --quiet
```

Start the containers:

```bash
docker compose \
  --file deploy/macos/compose.yaml \
  up --build --detach

docker compose \
  --file deploy/macos/compose.yaml \
  ps
```

The macOS profile builds a small local NGINX image with the public development
configuration copied into it. This avoids Docker Desktop failures while reading
a single-file bind mount from the macOS filesystem. No credential or private
environment value is copied into either image.

Wait for readiness:

```bash
until curl --fail --silent \
  http://127.0.0.1:8443/health/ready >/dev/null; do
  sleep 2
done

echo "Real-backend test profile is ready"
```

Readiness validates local configuration and storage. It intentionally does not request an OAuth token or contact Jamf; those operations begin with the first package miss.

## 3. Request one small existing package

Choose a small, non-sensitive, immutable package whose flat filename contains no spaces for the first test. Entering it through a prompt keeps it out of shell history:

```bash
printf 'Existing small .pkg filename: '
IFS= read -r package_name
```

Make the first request:

```bash
curl --fail --show-error \
  --dump-header /tmp/jcds-real-first.headers \
  --output /tmp/jcds-real-first.pkg \
  "http://127.0.0.1:8443/packages/${package_name}"

grep -Ei '^(HTTP/|X-Package-Source:|X-Request-ID:)' \
  /tmp/jcds-real-first.headers
```

Expected result:

```text
HTTP/1.1 200 OK
X-Package-Source: JCDS
X-Request-ID: ...
```

This single request proves that the helper obtained a real token, found exact catalog metadata, resolved `uri`, accepted the configured destination, streamed the object, matched the catalog length and SHA3-512 digest, and atomically published the completed package.

## 4. Confirm the local hit

Request the same package again:

```bash
curl --fail --show-error \
  --dump-header /tmp/jcds-real-second.headers \
  --output /tmp/jcds-real-second.pkg \
  "http://127.0.0.1:8443/packages/${package_name}"

grep -Ei '^(HTTP/|X-Package-Source:|X-Request-ID:)' \
  /tmp/jcds-real-second.headers

cmp /tmp/jcds-real-first.pkg /tmp/jcds-real-second.pkg \
  && echo "First and second responses match"
```

Expected source:

```text
X-Package-Source: LOCAL
```

The second request is served directly by NGINX and makes no Jamf or JCDS request.

## 5. Inspect privacy-safe behavior records

```bash
docker compose \
  --file deploy/macos/compose.yaml \
  logs --no-color nginx \
  | sed -n 's/^[^{]*//p'
```

The records should show one successful `JCDS` response followed by a `LOCAL` response. They exclude the package name, path, raw range, raw user agent, token, secret and signed URL.

If the request fails, note the HTTP status and request ID first. Inspect helper logs only on the Mac and do not paste them into a public channel because they may contain the package filename:

```bash
docker compose \
  --file deploy/macos/compose.yaml \
  logs --tail 100 cache-helper
```

Common controlled outcomes:

| Result | Likely cause |
|---|---|
| `404` | Filename is absent or does not exactly match the Jamf catalog/resolver entry |
| `502` | OAuth/Jamf failure, rejected destination, malformed response, object failure, or integrity mismatch |
| `504` | Backend operation exceeded a configured timeout |
| `507` | Docker Desktop test-volume free-space protection rejected the fill |

A failure must not create a reusable final file.

## 6. Stop or reset the test

Stop the containers while retaining the downloaded test package:

```bash
docker compose \
  --file deploy/macos/compose.yaml \
  down
```

Delete the Docker test volume and all packages stored in it only when intentionally resetting the real-backend test:

```bash
docker compose \
  --file deploy/macos/compose.yaml \
  down --volumes
```

Remove the two host-side response files when they are no longer needed:

```bash
rm -f /tmp/jcds-real-first.pkg /tmp/jcds-real-second.pkg \
  /tmp/jcds-real-first.headers /tmp/jcds-real-second.headers
```

Keep the private environment file for subsequent tests only if the Mac and its Docker administration boundary are approved for the Jamf credential. Otherwise, revoke or rotate the test client and securely remove the local file.
