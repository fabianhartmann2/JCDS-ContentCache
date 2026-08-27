# JCDS Content Cache

`JCDS-ContentCache` is a filesystem-backed pull-through package store for Jamf Cloud Distribution Service (JCDS). Managed clients use a stable internal URL such as:

```text
https://packages.example.ch:8443/packages/ExampleFile.pkg
```

NGINX serves complete packages directly from `/srv/jamf-store/packages/`. A cache miss is passed to a Go helper that obtains a Jamf OAuth token, resolves the temporary JCDS download URL, streams the package to the first client, writes it to hidden same-filesystem temporary storage, and atomically publishes the completed file under its original name.

> [!IMPORTANT]
> This repository is an early mock-driven implementation. It must not be connected to production Jamf credentials until the external API contract, destination allowlist, client range behavior, and security controls in the execution plan are validated.

## Current milestone

Milestone M1 demonstrates the complete local lifecycle without credentials:

1. First `GET` is routed through the helper.
2. Mock OAuth and resolver endpoints return a temporary object URL.
3. The helper begins streaming while it writes a hidden `.part` file.
4. A complete object is atomically published under its canonical filename.
5. The next request is served directly by NGINX.
6. Concurrent misses for the same filename share one upstream fill.

The v1 prototype accepts one filename segment ending in `.pkg`. This is a provisional implementation of open question OQ-11, not yet a final production decision.

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
go build ./cmd/cache-helper ./cmd/mock-upstream
```

CI also builds the container image. No test fixture contains a real token, secret, signed URL, or package.

## Repository map

```text
cmd/cache-helper/       Go service entry point
cmd/mock-upstream/      Credential-free development upstream
internal/auth/          OAuth token reuse and refresh
internal/config/        Environment parsing and validation
internal/download/      Download URL and redirect policy
internal/httpapi/       Health and package request handlers
internal/jamf/          Replaceable Jamf resolver adapter
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
- NGINX receives read-only access to completed package storage; the helper owns publication.
- Hidden temporary storage is outside the namespace served by NGINX.

## License

No open-source license has been selected yet. Until the repository owner adds one, normal copyright restrictions apply.

