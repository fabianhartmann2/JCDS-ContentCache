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
- Jamf catalog length and SHA3-512 metadata are verified before a downloaded file is published.
- MD5 is parsed for interoperability but is not used as the security integrity boundary.
- NGINX receives read-only access to completed package storage; the helper owns publication.
- Hidden temporary storage is outside the namespace served by NGINX.

## License

No open-source license has been selected yet. Until the repository owner adds one, normal copyright restrictions apply.
