# Jamf JCDS Package Cache — Project Execution Plan

**Status:** Active working plan  
**Version:** 0.2  
**Date:** 27 August 2026  
**Owner:** Mac Workplace  
**Target:** Production service on one managed Linux container host

## 1. Purpose and working agreement

This file is the implementation sequence for the Jamf JCDS filesystem-backed package caching server. It is intended to guide engineering work across future sessions and repository changes.

- `Jamf_JCDS_Package_Caching_Server_Requirements.md` is the normative product and technical specification.
- This execution plan controls implementation order, readiness gates, milestones and unresolved decisions.
- Work should proceed from the first incomplete phase unless a blocking dependency requires a documented exception.
- Each material design choice must update the decision register and, where applicable, the requirements document.
- A phase is complete only when its exit criteria are demonstrated by tests or recorded evidence.
- Credentials, access tokens and signed JCDS URLs must never be committed, included in fixtures or written to normal logs.

## 2. Confirmed baseline

| Topic | Confirmed decision |
|---|---|
| Delivery target | Production service |
| Package identity | Filenames are immutable; one filename always identifies the same bytes |
| Deployment | NGINX and a Go helper run as containers on one Linux host |
| Client namespace | `https://<server>:8443/packages/<filename>.pkg` |
| Local storage | URL-derived, filename-preserving files below `/srv/jamf-store/packages/` |
| Local hit | NGINX serves the completed file directly |
| Store miss | NGINX routes internally to the helper |
| Upstream access | OAuth 2.0 client credentials, followed by the Jamf file-resolution API and temporary JCDS URL |
| First requester | Receives a streamed response while the store is being filled |
| Publication | Download into hidden same-filesystem temporary storage; validate; atomically rename |
| Concurrency | One upstream transfer per canonical package while concurrent callers wait or share the coordinated result |
| Cache model | Do not use NGINX's opaque hashed `proxy_cache` as the authoritative store |
| Repository | Public GitHub repository [`fabianhartmann2/JCDS-ContentCache`](https://github.com/fabianhartmann2/JCDS-ContentCache) |

## 3. Implementation principles

1. Build a thin end-to-end slice before adding production hardening.
2. Keep Jamf-specific behavior behind an adapter interface because the referenced endpoint is deprecated.
3. Treat the filesystem as a publication boundary: clients may only see complete final files.
4. Normalize and validate a package name before any filesystem lookup or upstream request.
5. Stream bytes; never buffer a complete package in memory.
6. Make failure behavior explicit, bounded and observable.
7. Prefer integration tests with local mock OAuth, Jamf API and object-download servers.
8. Require real-tenant evidence only where mocks cannot establish the external contract.

## 4. Delivery phases

### Phase 0 — Validate external contracts

**Goal:** Remove external unknowns that could invalidate the helper or NGINX design.

- [ ] Create a dedicated read-only Jamf API client for development.
- [ ] Confirm the supported file-resolution endpoint in the target tenant's `/api/doc`.
- [ ] Capture a redacted successful file-resolution JSON response.
- [ ] Capture redacted not-found, unauthorized, rate-limit and server-error responses.
- [ ] Record the precise field containing the temporary download URL.
- [ ] Record OAuth token response fields, expiry behavior and relevant error responses.
- [ ] Identify approved JCDS hostnames and every permitted redirect destination.
- [ ] Determine whether object responses expose `Content-Length`, `ETag`, `Last-Modified`, checksums and range support.
- [ ] Capture representative Mac client requests for `GET`, `HEAD`, single-range resume and any multi-range behavior.
- [ ] Confirm whether the first store-miss request will always fetch a complete object even when the client requests a range.
- [ ] Confirm the v1 path model: one filename segment ending in `.pkg`, or nested subdirectories.
- [ ] Record findings in `docs/external-contracts.md`; include only sanitized examples.

**Exit criteria**

- The helper's Jamf adapter inputs, outputs and failure mapping are unambiguous.
- The egress allowlist and redirect rules can be configured without accepting arbitrary destinations.
- The range-request strategy is testable.
- No production secret is present in project files.

### Phase 1 — Build the vertical slice

**Goal:** Demonstrate the complete miss-to-local-hit lifecycle locally.

- [x] Initialize the Go module and service entry point.
- [x] Add strict configuration parsing and startup validation.
- [x] Implement canonical package-name validation.
- [ ] Implement an in-memory OAuth token provider with an expiry safety margin.
- [ ] Retry one Jamf API request after a `401` by invalidating and refreshing the token.
- [ ] Define a replaceable Jamf file-resolver interface.
- [ ] Validate resolved URLs against HTTPS, hostname and redirect policy.
- [ ] Implement streaming object download without whole-file memory buffering.
- [ ] Implement same-filesystem temporary files and atomic publication.
- [ ] Implement per-package single-flight coordination.
- [ ] Decide and test whether an upstream fill continues after the initiating client disconnects.
- [x] Configure NGINX `try_files` for local hits and an internal helper route for misses.
- [x] Add Dockerfiles and a local Docker Compose stack.
- [ ] Add mock OAuth, Jamf resolver and object-download services for integration tests.
- [ ] Add structured logs with automatic sensitive-field exclusion.
- [x] Add basic liveness and readiness endpoints.

**Milestone M1 acceptance evidence**

- [ ] The first request starts receiving bytes before the complete object reaches the cache host.
- [ ] A completed download appears at the deterministic final path and matches the source bytes.
- [ ] The second request is served locally without OAuth, Jamf API or object-download calls.
- [ ] Concurrent misses for one package cause one upstream object transfer.
- [ ] An interrupted or corrupt transfer never appears at the final public path.
- [ ] A client abort behaves according to the recorded policy.
- [ ] Restarting the containers preserves and serves completed packages.

### Phase 2 — Security and failure handling

**Goal:** Make the vertical slice safe and predictable under hostile input and dependency failures.

- [ ] Enforce method, path length, character and extension restrictions.
- [ ] Reject traversal, encoded traversal, ambiguous encoding, absolute URLs, query-based destinations and symlink escapes.
- [ ] Apply inbound network controls and the selected client-authentication policy.
- [ ] Apply outbound DNS, host, port and redirect restrictions.
- [ ] Deliver the Jamf client secret through the selected secret platform.
- [ ] Ensure secrets, tokens and signed URLs are redacted from logs, metrics, traces and error responses.
- [ ] Add explicit downstream error mapping for validation, not found, authentication, throttling, timeout and upstream failure cases.
- [ ] Add bounded connect, header, read, idle and total-operation timeouts.
- [ ] Add bounded retries with backoff only for safe transient operations.
- [ ] Add maximum object size and minimum-free-space protections.
- [ ] Add temporary-file cleanup after failure, restart and age threshold.
- [ ] Define checksum or signature validation policy and implement available metadata checks.
- [ ] Add non-root containers, read-only root filesystems where practical and minimal Linux capabilities.
- [ ] Add dependency, image and source scanning to CI.

**Exit criteria**

- Security and resilience acceptance tests pass.
- A threat review finds no route to arbitrary filesystem access or arbitrary upstream fetching.
- Secret scanning confirms that repository history and build artifacts contain no credentials.

### Phase 3 — Production integration

**Goal:** Deploy a controlled production candidate using real enterprise services.

- [ ] Provision the Linux host, persistent volume, DNS, firewall and egress rules.
- [ ] Issue and automatically renew the service certificate.
- [ ] Provision the least-privilege Jamf API client in the enterprise secret store.
- [ ] Configure capacity thresholds, retention and administrative cleanup procedures.
- [ ] Connect logs, metrics and alerts to the selected monitoring platform.
- [ ] Create operational dashboards for requests, local hits, fills, failures, latency, OAuth health, active downloads and disk state.
- [ ] Add backup/rebuild expectations and a tested disaster-recovery procedure.
- [ ] Run a real-tenant smoke test with approved non-sensitive packages.
- [ ] Measure throughput, time to first byte, CPU, memory, disk I/O and WAN usage.
- [ ] Tune timeouts and concurrency using measured package sizes and client demand.
- [ ] Complete security, infrastructure and service-owner reviews.

**Exit criteria**

- A release candidate runs in the target environment with all production dependencies.
- Monitoring, alerting and runbooks are usable by the support owner.
- Measured capacity satisfies the agreed workload and availability targets.

### Phase 4 — Acceptance and rollout

**Goal:** Prove the requirements and introduce the service safely.

- [ ] Execute all acceptance tests from the requirements specification.
- [ ] Record evidence for every production acceptance gate.
- [ ] Test local-hit service during a simulated Jamf/JCDS outage.
- [ ] Test disk-low, disk-full, token expiry, redirect rejection, upstream timeout and process-restart scenarios.
- [ ] Pilot with a small managed-client group.
- [ ] Compare client success, latency, bandwidth savings and error rates against the baseline.
- [ ] Document rollback criteria and test the rollback path.
- [ ] Resolve pilot defects and obtain production approval.
- [ ] Expand rollout in controlled stages.
- [ ] Hold a post-rollout review and update the backlog and runbooks.

**Exit criteria**

- All mandatory acceptance gates pass.
- Service ownership and on-call/escalation paths are documented.
- Production rollout is approved and observable.

## 5. Open-question register

Status values are `OPEN`, `IN REVIEW` or `RESOLVED`. Blocking questions must be resolved before the affected implementation or production gate.

| ID | Decision | Priority | Current status | Recommended starting position | Evidence needed |
|---|---|---:|---|---|---|
| OQ-01 | Exact supported Jamf file-resolution API contract | Blocking | OPEN | Use the target tenant's `/api/doc`; isolate it behind an adapter | Redacted success and error responses, endpoint/version confirmation |
| OQ-05 | Client and upstream range-request behavior | Blocking | OPEN | Capture real client traffic; on a miss fetch and publish the full object | `GET`, `HEAD`, resume and multi-range captures; JCDS response behavior |
| OQ-06 | Permitted JCDS hosts and redirects | Blocking | OPEN | Explicit HTTPS hostname allowlist; revalidate each redirect | Production host/redirect inventory from tenant behavior and Jamf documentation |
| OQ-11 | Flat filenames or nested paths | Blocking | OPEN | V1 accepts one filename segment ending in `.pkg` | Required package naming examples and collision analysis |
| OQ-12 | Integrity source of truth | High | OPEN | Require complete transfer and valid package signatures initially; use upstream checksum when available | JCDS metadata/header inventory and enterprise validation policy |
| OQ-03 | Package workload and concurrency | High | OPEN | Measure before fixing performance limits | Package count/size distribution, peak clients, common simultaneous requests |
| OQ-07 | Store capacity and retention | High | OPEN | Treat 500 GB and 180 days as provisional only | Inventory growth, reuse interval, disk budget and operational cleanup policy |
| OQ-02 | Client access control | High | OPEN | Server TLS plus enterprise-network allowlist; add mTLS if network trust is insufficient | Client network paths, proxy/VPN behavior and security policy |
| OQ-04 | Availability and service-level objective | Medium | OPEN | Provisional 99.5%, excluding approved maintenance, for the single-host release | Business impact, maintenance window and recovery expectations |
| OQ-08 | TLS certificate ownership | Medium | OPEN | Enterprise DNS and automatically renewed enterprise PKI certificate | DNS owner, certificate platform and renewal responsibility |
| OQ-09 | Secret delivery platform | Medium | OPEN | Read-only delivery from the enterprise secret store; no secret in images or Compose files | Available platform, rotation method and runtime integration |
| OQ-10 | Monitoring and alerting platform | Medium | OPEN | Use the existing enterprise platform and expose Prometheus-compatible metrics where supported | Platform, log format, metric scraping and alert ownership |

## 6. Definition of ready for coding

Repository scaffolding and mock-driven implementation can begin immediately. Real Jamf adapter completion is ready when:

- [ ] OQ-01 has a sanitized but structurally complete API contract.
- [ ] OQ-05 has enough evidence to select the miss/range policy.
- [ ] OQ-06 has an enforceable destination and redirect allowlist.
- [ ] OQ-11 fixes the canonical path model.
- [x] The repository owner, name and public visibility are confirmed.
- [x] A GitHub connection with permission to create or write the repository is available.

OQ-12 may initially use the recommended starting position if upstream checksum metadata is not available. Capacity, availability, TLS, secrets and monitoring questions must be resolved before Phase 3.

## 7. Initial repository layout

```text
.
├── cmd/cache-helper/          # Go service entry point
├── internal/auth/             # OAuth token provider
├── internal/config/           # Configuration and validation
├── internal/download/         # Stream and redirect policy
├── internal/httpapi/          # Helper HTTP endpoints and error mapping
├── internal/jamf/             # Replaceable Jamf adapter
├── internal/store/            # Locks, temporary files and atomic publication
├── deploy/compose/            # Local and single-host deployment
├── deploy/nginx/              # NGINX configuration
├── docs/                      # Requirements, decisions, contracts and runbooks
├── tests/integration/         # Mock-backed end-to-end tests
├── .github/workflows/         # Test, lint, build and security checks
├── Dockerfile
├── go.mod
├── LICENSE
└── README.md
```

## 8. First repository milestone

The first coding milestone is a local, credential-free demonstration using mock upstream services.

**Deliverables**

- Buildable Go helper with strict configuration.
- NGINX configuration using filename-preserving local storage.
- Docker Compose development stack.
- Mock OAuth, resolver and package endpoints.
- Integration test for miss, streaming fill, atomic publication and subsequent local hit.
- Integration test for concurrent requests producing one upstream transfer.
- README with setup, test and architecture notes.
- Copies of the requirements and this execution plan under `docs/`.

**Not required for the first milestone**

- Production credentials.
- A live Jamf connection.
- Enterprise DNS, PKI, secrets or monitoring integrations.
- Final production capacity values.

## 9. Decision and change log

| Date | Item | Change | State |
|---|---|---|---|
| 2026-08-27 | Architecture | Selected NGINX plus a Go helper on one Linux container host | Resolved |
| 2026-08-27 | Storage | Selected a filename-preserving filesystem store with hidden temporary files and atomic publication | Resolved |
| 2026-08-27 | Package identity | Confirmed immutable filenames | Resolved |
| 2026-08-27 | Execution | Established contract validation, vertical slice, hardening, production integration and rollout phases | Active |
| 2026-08-27 | Repository | Confirmed public repository `fabianhartmann2/JCDS-ContentCache` and write access | Resolved |
| 2026-08-27 | Foundation | Published the Go/NGINX/Compose skeleton; initial GitHub CI passed | Complete |

## 10. Immediate next actions

1. Implement and test the in-memory OAuth client-credentials token provider against a local mock endpoint.
2. Define the replaceable Jamf file-resolver interface and sanitized response fixtures.
3. Add mock resolver and object-download services for the first integration test.
4. Enable default-branch protection and require the passing CI workflow when repository settings permit.
5. Obtain a redacted successful Jamf file-resolution JSON response as the first external-contract artifact.
