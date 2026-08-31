# JCDS Content Cache

`JCDS-ContentCache` is a filesystem-backed pull-through package store for Jamf Cloud Distribution Service (JCDS). Managed clients use a stable internal URL such as:

```text
https://jcds-cache.appfruit.ch:8443/packages/ExampleFile.pkg
```

NGINX serves complete packages directly from `/srv/jamf-store/packages/`. A cache miss is passed to a Go helper that obtains a Jamf OAuth token, retrieves authoritative size and SHA3-512 metadata, resolves the temporary JCDS download URL, streams the package to the first client, writes it to hidden same-filesystem temporary storage, and atomically publishes the completed file under its original name only after integrity validation succeeds.

> [!IMPORTANT]
> The production target is a dedicated 24 GB/1 TB Mac mini running Docker Desktop, but the repository does not yet contain an approved LAN-facing macOS production profile. Use real Jamf credentials only with the localhost integration profile or a reviewed production deployment, and always configure an exact JCDS hostname allowlist. Unattended startup, APFS bind-mount qualification, LAN source-IP enforcement, certificate renewal, retention and monitoring ownership remain production gates.

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
14. NGINX emits privacy-safe JSON behavior records for package requests without logging package names, paths, query strings, raw `Range` values, or raw user agents.

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

The local stack intentionally uses plain HTTP on host port `8443`. The production-candidate stack uses TLS and the host-specific certificate paths in the production NGINX template.

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

## Real-backend test on macOS

After the mock stack passes, Docker Desktop on a Mac can run a localhost-only integration profile against the real Jamf/JCDS backend. It uses a Docker-managed test volume, validates all upstream TLS connections, injects credentials from a private file outside Git, and publishes plain HTTP only on `127.0.0.1:8443`. It is an integration-test profile, not a production service or LAN listener.

Follow [Real-backend test on macOS](docs/macos-real-backend-test.md). Never commit or share the completed environment file, tenant hostname, exact JCDS hostname, signed URL, token, secret, or unsanitized helper log.

## macOS production target

The first production target is a dedicated Mac running Docker Desktop and serving `jcds-cache.appfruit.ch:8443` to `192.168.0.0/16`. NGINX and the Go helper continue to run inside Docker Desktop's Linux VM. The existing `deploy/macos/` profile is deliberately bound to localhost and is not the production listener.

The macOS production design requires a separate TLS-enabled Compose profile, managed Docker Desktop startup and updates, explicit VM resource sizing, production storage and recovery procedures. Production selects a Finder-visible APFS bind mount, subject to a mandatory Docker Desktop reliability qualification. NGINX is intended to enforce the client CIDR, but this remains blocked until a LAN test proves that Docker Desktop preserves trustworthy client source addresses. The package store remains derived and rebuildable from JCDS.

See [Production architecture](docs/architecture.md) for confirmed boundaries and blocking decisions, and [Production deployment](docs/production-deployment.md) for the current readiness plan. Do not expose the localhost test profile to the LAN or copy a completed environment file, real Jamf tenant URL, signed download URL, or exact production JCDS hostname into GitHub.

## Client request monitoring

NGINX writes one structured JSON record to standard output for each request under `/packages/`. The record distinguishes `GET` and `HEAD`, full and range requests, local hits and upstream fills, response status, transferred bytes, timing, completion, and a coarse client class. `X-Request-ID` is returned to the client and forwarded to the helper for correlation.

The standard behavior log intentionally excludes the URI, package name, query string, raw `Range`, raw `User-Agent`, authorization, cookies, and referrer. The source IP remains present so operators can correlate request sequences from a managed client; it must therefore receive access controls and retention appropriate for client-identifying operational data.

Inspect development records with:

```bash
docker compose -f deploy/compose/docker-compose.yml logs --no-color nginx \
  | sed -n 's/^[^{]*//p' \
  | jq -c 'select(.event == "package_request")'
```

See [Client request monitoring](docs/client-request-monitoring.md) for the schema, privacy boundary, request classifications, example analyses, proxy caveats, and production guidance.

## Sanitized live-contract capture

After a dedicated read-only Jamf API client is available, run the Docker wrapper from an interactive terminal:

```bash
./scripts/capture-live-contracts.sh > sanitized-contract-report.json
```

The wrapper prompts for the Jamf base URL, read-only API client ID, hidden client secret, one existing flat `.pkg` filename, and an optional enterprise CA bundle. It builds an unprivileged one-shot image, runs with a read-only root filesystem and all Linux capabilities dropped, removes the container afterward, and applies a final disclosure guard before printing the report. Values entered at the prompts are not placed in shell history or written to the report. Like any Docker environment value, they remain inspectable by a local Docker administrator while the short-lived container is running.

The live validation performs one OAuth request, one catalog request, one resolver request, one object `HEAD`, and one `GET` with `Range: bytes=0-0`. If the object endpoint ignores the range and returns `200`, the client closes the body immediately rather than intentionally downloading the complete package. The generated JSON contains only:

- OAuth field types, token type and lifetime
- JCDS file count and aggregate v1 `.pkg` count, total bytes and largest bytes
- metadata-presence and digest-length checks for the selected package
- truncated SHA-256 hostname fingerprints, query-parameter count and redirect observations
- status and capability observations for `HEAD` and the one-byte range probe

It does not contain the access token, client secret, tenant hostname, selected package name, package digests, signed URL, object path, query values, ETag or Last-Modified value. The report filename pattern is ignored by Git, but the report should still be reviewed before it is shared.

If outbound access requires a static proxy, export `HTTPS_PROXY` and, if needed, `NO_PROXY` before running the wrapper. The container inherits those values for the capture only. Networks that inspect TLS must also provide the inspecting enterprise root or intermediate certificates as a PEM file at the optional prompt, or export its path as `CAPTURE_CA_CERT_FILE`. The wrapper leaves the original untouched, creates a short-lived read-only copy for the non-root container, combines it in memory-backed temporary storage with the public CA bundle, and deletes the copy during cleanup. The CA is never copied into the image or report.

The capture image performs no package-repository download during its final build stage. Its public CA bundle is copied from the pinned Go builder image, which avoids failures caused by TLS inspection of Alpine package repositories.

Developers with Go 1.24 may run `go run ./cmd/contract-capture` directly after exporting the six environment variables shown in `deploy/contract-capture/compose.yaml`.

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
deploy/contract-capture/ Hardened one-shot live validation image
deploy/macos/           Localhost-only real-backend Docker Desktop test
deploy/nginx/           Development and production NGINX templates
deploy/production/      Superseded Linux candidate retained pending macOS replacement
docs/                   Architecture, requirements, execution plan and contract evidence
```

## Documentation

- [Technical requirements](docs/requirements.md)
- [Production architecture](docs/architecture.md)
- [Project execution plan](docs/execution-plan.md)
- [External-contract evidence template](docs/external-contracts.md)
- [Client request monitoring](docs/client-request-monitoring.md)
- [Real-backend test on macOS](docs/macos-real-backend-test.md)
- [Production deployment readiness](docs/production-deployment.md)

## Security notes

- Never commit Jamf client credentials, OAuth tokens, temporary signed URLs, or unsanitized API responses.
- The helper accepts only a validated package filename and never accepts a client-supplied upstream URL.
- Every resolved URL and redirect is checked against the configured hostname allowlist.
- In production mode, every allowed download hostname is resolved before use and the request is rejected if any returned address is private, loopback or link-local.
- Dependency response bodies and complete request URLs are excluded from propagated errors so temporary signed queries cannot enter normal logs.
- NGINX behavior logs omit package identity and raw request headers; standard per-request error logging is suppressed because NGINX error messages can contain the raw request line.
- Jamf catalog length and SHA3-512 metadata are verified before a downloaded file is published.
- MD5 is parsed for interoperability but is not used as the security integrity boundary.
- NGINX receives read-only access to completed package storage; the helper owns publication.
- Hidden temporary storage is outside the namespace served by NGINX.

## License

No open-source license has been selected yet. Until the repository owner adds one, normal copyright restrictions apply.
