# JCDS Content Cache

Filesystem-backed pull-through package delivery for Jamf Cloud Distribution Service (JCDS).

> **Status:** Early implementation. The current helper validates configuration, exposes health endpoints and rejects unsafe package paths. OAuth, Jamf resolution, streaming download and atomic publication are the next vertical-slice work.

## Intended behavior

Clients request a stable internal URL such as:

```text
GET https://packages.example.invalid:8443/packages/ExampleFile.pkg
```

NGINX serves a completed package directly from:

```text
/srv/jamf-store/packages/ExampleFile.pkg
```

On a miss, NGINX calls the internal Go helper. The helper will obtain a short-lived OAuth token, resolve a temporary JCDS URL, validate its destination, stream the object to the first client, and atomically publish the completed file for later local delivery.

## Current repository contents

- `cmd/cache-helper`: service entry point and graceful shutdown
- `internal/config`: strict non-secret configuration
- `internal/pathpolicy`: v1 package filename validation
- `internal/httpapi`: health endpoints and safe miss-request validation
- `deploy/nginx`: local-hit and helper-miss routing
- `deploy/compose`: local two-container development stack
- `docs`: requirements and phased execution plan

## Local development

Requirements:

- Go version declared in `go.mod`
- Docker with Compose for the two-container stack

Run unit tests:

```console
go test ./...
```

Start the development stack:

```console
docker compose -f deploy/compose/compose.yaml up --build
```

Check the helper through NGINX:

```console
curl --fail http://localhost:8081/livez
curl --fail http://localhost:8081/readyz
```

The development listener is intentionally plain HTTP on port 8081. Production TLS, enterprise access controls and credentials will be added only after the relevant open questions are resolved.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `CACHE_HELPER_LISTEN_ADDRESS` | `:8080` | Internal helper listener |
| `CACHE_STORE_ROOT` | `/srv/jamf-store` | Absolute root of the filename-preserving package store |

No credentials belong in this repository. Future Jamf client secrets must be supplied by the selected runtime secret mechanism.

## Documentation

- `docs/requirements.md` — normative product and technical requirements
- `docs/execution-plan.md` — active phase plan, gates and open-question register

## License

No license has been selected yet. Until one is added, normal copyright rules apply.
