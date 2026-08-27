# JCDS Content Cache

`JCDS-ContentCache` is a filesystem-backed pull-through package store for Jamf Cloud Distribution Service (JCDS). Managed clients use a stable internal URL such as:

```text
https://packages.example.ch:8443/packages/ExampleFile.pkg
```

NGINX serves complete packages directly from `/srv/jamf-store/packages/`. A cache miss is passed to a Go helper that obtains a Jamf OAuth token, retrieves authoritative size and SHA3-512 metadata, resolves the temporary JCDS download URL, streams the package to the first client, writes it to hidden same-filesystem temporary storage, and atomically publishes the completed file under its original name only after integrity validation succeeds.

> [!IMPORTANT]
> This repository is an early mock-driven implementation. It must not be connected to production Jamf credentials until the external API contract, destination allowlist, client range behavior, and security controls in the execution plan are validated.

## Current milestone

Milestone M1 demonstrates the complete local lifecycle without credentials:

1. First `GET` is routed through the helper.
2. Mock OAuth and catalog endpoints return package length and SHA3-512 metadata.
3. The mock resolver returns a temporary object URL.
4. The helper begins streaming while it writes and hashes a hidden `.part` file.
5. A length- and SHA3-validated object is atomically published under its canonical filename.
6. The next request is served directly by NGINX.
7. Concurrent misses for the same filename share one upstream fill.
8. Client disconnects do not cancel an active, bounded cache fill.
9. Truncated or digest-mismatched transfers are discarded rather than published.
10. Completed packages survive helper and NGINX container restarts.
11. A range request on a miss retrieves the complete object; local hits support normal `206 Partial Content` delivery.
12. Local packages remain available during a simulated Jamf/JCDS outage, while a missing package receives a controlled `502` response.
13. OAuth, Jamf API, redirect, and object failures are categorized without returning dependency bodies or logging complete request URLs.

The confirmed v1 contract accepts exactly one flat filename segment ending in lowercase `.pkg`. Nested paths and additional file types are deliberately outside the first release. Initial sizing targets 500–2,000 managed Macs and 500 GB–1 TB of usable cache storage with at least 20 percent operational headroom.

## Local demonstration

Requirements: Docker Engine with the Compose plugin and `curl`.

```bash
docker compose -f deploy/compose/docker-compose.yml up --build -d
curl --fail --show-error --dump-header - \
  http://localhost:8443/packages/ExampleFile.pkg \
  --output /tmp/ExampleFile.pkg
curl --fail --show-error --dump-header - \
  http://localhost:8443/packages/ExampleFile.pkg \
  --output /tmp/ExampleFile-second.pkg
cmp /tmp/ExampleFile.pkg /tmp/ExampleFile-second.pkg
```

Expected response headers:

- First request: `X-Package-Source: JCDS`
- Subsequent request: `X-Package-Source: LOCAL`

The local stack intentionally uses plain HTTP on host port `8443`. Production deployment must use the TLS template and enterprise-managed certificates.

Stop and remove the development stack:

```bash
docker compose -f deploy/compose/docker-compose.yml down
```

Add `-v` only when you intentionally want to delete the local package-store volume.

## Test and build

```bash
go test -race ./...
go vet ./...
go build ./cmd/cache-helper ./cmd/mock-upstream ./cmd/contract-capture
```

CI also builds the container image. No test fixture contains a real token, secret, signed URL, or package.

Run the deployed-path smoke test separately with Docker:

```bash
tests/integration/compose_smoke.sh
```

It proves the NGINX/helper `JCDS`-to-`LOCAL` transition, verifies that repeated and range requests make no additional upstream calls, restarts both serving containers, and confirms that the completed package remains locally available after restart and during an upstream outage.

## Sanitized live-contract capture

After a dedicated read-only Jamf API client is available, an administrator can validate the live success contracts without printing the access token, client secret, tenant hostname, package name, package size, hashes, signed URL, object path or query values:

```bash
export JAMF_TOKEN_URL="https://<tenant-host>/api/v1/oauth/token"
export JAMF_CLIENT_ID="<client-id>"
export JAMF_CLIENT_SECRET="<client-secret>"
export JAMF_CATALOG_URL="https://<tenant-host>/api/v1/jcds/files"
export JAMF_RESOLVER_URL_TEMPLATE="https://<tenant-host>/api/v1/jcds/files/{filename}"
export CAPTURE_PACKAGE_NAME="<existing-flat-package-name>.pkg"

go run ./cmd/contract-capture > sanitized-contract-report.json
```

Run this locally; do not commit the environment values or raw API responses. The generated report contains only JSON field types, expiry seconds, metadata-presence checks, digest lengths and a truncated SHA-256 hostname fingerprint for comparing destination stability.

## Repository map

```text
cmd/cache-helper/       Go service entry point
cmd/mock-upstream/      Credential-free development upstream
cmd/contract-capture/   Sanitized live-contract validation tool
internal/auth/          OAuth token reuse and refresh
internal/config/        Environment parsing and validation
internal/download/      Download URL and redirect policy
internal/httpapi/       Health and package request handlers
internal/jamf/          Replaceable Jamf resolver and metadata-catalog adapters
internal/store/         Temporary files, publication and single-flight locks
deploy/compose/         Local development stack
deploy/nginx/           Development and production NGINX templates
docs/                   Requirements, execution plan and contract evidence
```

## Documentation

- [Technical requirements](docs/requirements.md)
- [Project execution plan](docs/execution-plan.md)
- [External-contract evidence template](docs/external-contracts.md)

## Security notes

- Never commit Jamf client credentials, OAuth tokens, temporary signed URLs, or unsanitized API responses.
- The helper accepts only a validated package filename and never accepts a client-supplied upstream URL.
- Every resolved URL and redirect is checked against the configured hostname allowlist.
- In production mode, every allowed download hostname is resolved before use and the request is rejected if any returned address is private, loopback or link-local.
- Dependency response bodies and complete request URLs are excluded from propagated errors so temporary signed queries cannot enter normal logs.
- Jamf catalog length and SHA3-512 metadata are verified before a downloaded file is published.
- MD5 is parsed for interoperability but is not used as the security integrity boundary.
- NGINX receives read-only access to completed package storage; the helper owns publication.
- Hidden temporary storage is outside the namespace served by NGINX.

## License

No open-source license has been selected yet. Until the repository owner adds one, normal copyright restrictions apply.
