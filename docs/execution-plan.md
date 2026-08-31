# Jamf JCDS Package Cache — Project Execution Plan

**Status:** Active working plan  
**Version:** 0.7  
**Date:** 30 August 2026  
**Owner:** Mac Workplace  
**Target:** Production service on one dedicated Mac running Docker Desktop

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
| Deployment | NGINX and a Go helper run as containers through Docker Desktop on one dedicated Mac |
| Client namespace | `https://<server>:8443/packages/<filename>.pkg` |
| V1 path scope | Exactly one flat filename segment ending in lowercase `.pkg`; no nested paths or additional file types |
| Initial client population | 500–2,000 managed Macs |
| Initial usable cache storage | 500 GB–1 TB, retaining 20% operational headroom |
| Host baseline | Dedicated Mac on a supported macOS release; exact hardware and resources remain open |
| Service endpoint | `https://jcds-cache.appfruit.ch:8443` |
| Client access | Server-authenticated TLS and source CIDR `192.168.0.0/16`; no additional v1 client authentication |
| Package-store mount | Docker named volume at `/srv/jamf-store`; macOS administrative visibility and production qualification pending |
| DNS and certificate | Manual DNS records and an initial certificate obtained through manual DNS validation; unattended renewal remains a production gate |
| Secret delivery | Root-owned host environment file, mode `0600`, passed to the helper by Docker Compose |
| Outbound network | Direct HTTPS; no proxy or TLS inspection |
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
2. Use the selected deprecated JCDS endpoints until Jamf introduces replacements, and keep Jamf-specific behavior behind adapter interfaces so migration does not alter client URLs.
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
- [x] Record the decision to use deprecated `GET /api/v1/jcds/files/{fileName}` until Jamf introduces a replacement.
- [x] Capture a redacted successful file-resolution JSON response.
- [x] Capture the redacted resolver not-found status and JSON shape.
- [ ] Capture redacted unauthorized, rate-limit and server-error responses.
- [x] Record the precise field containing the temporary download URL (`uri`).
- [x] Capture sanitized JCDS file-list metadata fields: `fileName`, `length`, `md5`, `region`, and `sha3`.
- [x] Confirm that the complete file-list response is a top-level JSON array without a pagination envelope.
- [x] Record OAuth token success fields and observed expiry behavior; relevant error responses remain open.
- [ ] Identify approved JCDS hostnames and every permitted redirect destination.
- [x] Establish JCDS catalog `length` and SHA3-512 as the publication-integrity source of truth.
- [ ] Determine whether object responses expose `Content-Length`, `ETag`, `Last-Modified`, and range support.
- [ ] Capture representative Mac client requests for `GET`, `HEAD`, single-range resume and any multi-range behavior.
- [x] Implement and test the provisional rule that a store miss always fetches a complete object even when the client requests a range.
- [x] Confirm the v1 path model as one filename segment ending in lowercase `.pkg`, without nested subdirectories or additional file types.
- [x] Record current findings in `docs/external-contracts.md`; include only sanitized examples.
- [x] Add a Dockerized contract-capture workflow that emits only schema types, expiry seconds, aggregate package sizing, metadata-presence checks, digest lengths, hostname fingerprints, redirect counts, and safe HEAD/range capability observations; support a read-only, user-supplied PEM CA bundle for TLS-inspecting networks.

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
- [x] Implement an in-memory OAuth token provider with an expiry safety margin.
- [x] Retry one Jamf API request after a `401` by invalidating and refreshing the token.
- [x] Define a replaceable Jamf file-resolver interface.
- [x] Add a replaceable JCDS metadata-catalog interface and strict response validation.
- [x] Validate resolved URLs against HTTPS, hostname and redirect policy.
- [x] Implement streaming object download without whole-file memory buffering.
- [x] Implement same-filesystem temporary files and atomic publication.
- [x] Implement per-package single-flight coordination.
- [x] Continue a bounded upstream fill after the initiating client disconnects, and test successful subsequent local delivery.
- [x] Configure NGINX `try_files` for local hits and an internal helper route for misses.
- [x] Add Dockerfiles and a local Docker Compose stack.
- [x] Add mock OAuth, Jamf resolver and object-download services for integration tests.
- [x] Add structured logs with automatic sensitive-field exclusion.
- [x] Add privacy-safe NGINX client-behavior records for method, range class, response source/status, bytes, timing and completion, with request IDs and automated disclosure tests.
- [x] Add basic liveness and readiness endpoints.
- [x] Verify catalog length and SHA3-512 before atomic publication.

**Milestone M1 acceptance evidence**

- [x] The first request starts receiving bytes before the complete object reaches the cache host.
- [x] A completed download appears at the deterministic final path and matches the source bytes.
- [x] The second request is served locally without OAuth, Jamf API or object-download calls.
- [x] Concurrent misses for one package cause one upstream object transfer.
- [x] An interrupted or corrupt transfer never appears at the final public path.
- [x] A client abort behaves according to the recorded policy.
- [x] Restarting the containers preserves and serves completed packages.

### Phase 2 — Security and failure handling

**Goal:** Make the vertical slice safe and predictable under hostile input and dependency failures.

- [x] Enforce method, path length, character and extension restrictions for the confirmed flat lowercase `.pkg` namespace.
- [x] Reject traversal, encoded traversal, ambiguous encoding, absolute URLs, query-based destinations and symlink escapes.
- [x] Add production-candidate NGINX controls for TLS and the confirmed `192.168.0.0/16` client allowlist; host/perimeter enforcement remains a deployment task.
- [x] Apply exact-host, HTTPS-only, DNS-address and per-redirect restrictions in the helper; the final runtime hostname inventory remains open.
- [x] Define root-owned host environment-file delivery for the Jamf secret without placing credentials in images, Compose YAML or Git.
- [x] Ensure secrets, tokens and signed URLs are redacted from normal logs and error responses, with automated disclosure-focused tests.
- [x] Sanitize dependency response bodies and complete request URLs before errors reach client responses or normal logs.
- [x] Add explicit downstream error mapping for validation, not found, authentication, throttling, timeout and upstream failure cases.
- [x] Add bounded connect, TLS-handshake, response-header, idle, fill and shutdown timeouts.
- [ ] Add bounded retries with backoff only for safe transient operations.
- [x] Add maximum object size and minimum-free-space protections.
- [x] Add startup cleanup for stale regular `.part` files after the configured age threshold.
- [x] Require Jamf catalog length and SHA3-512 validation before publishing a downloaded package; retain MD5 for interoperability only.
- [x] Add non-root helper/mock images plus production read-only root filesystems, capability dropping, PID/memory bounds and unprivileged internal networking.
- [ ] Add dependency, image and source scanning to CI.

**Exit criteria**

- Security and resilience acceptance tests pass.
- A threat review finds no route to arbitrary filesystem access or arbitrary upstream fetching.
- Secret scanning confirms that repository history and build artifacts contain no credentials.

### Phase 3 — Production integration

**Goal:** Deploy a controlled production candidate using real enterprise services.

- [ ] Provision the dedicated Mac, managed Docker Desktop, persistent storage, DNS, NGINX source-CIDR enforcement and egress rules.
- [x] Add a host-specific Compose definition, NGINX TLS configuration, environment templates, certificate check and deployment/rollback runbook.
- [ ] Issue the initial certificate through manual DNS validation and establish an automated renewal method before production approval.
- [ ] Provision the least-privilege Jamf API client and install its secret in the root-owned host environment file.
- [ ] Configure capacity thresholds, retention and administrative cleanup procedures.
- [ ] Connect logs, metrics and alerts to the selected monitoring platform.
- [ ] Create operational dashboards for requests, local hits, fills, failures, latency, OAuth health, active downloads and disk state.
- [ ] Add backup/rebuild expectations and a tested disaster-recovery procedure.
- [ ] Run a real-tenant smoke test with approved non-sensitive packages.
- [x] Add a localhost-only Docker Desktop profile and credential-safe runbook for the controlled real-tenant smoke test on macOS.
- [x] Validate a real Jamf/JCDS store miss followed by a byte-identical local hit on Docker Desktop, with privacy-safe monitoring.
- [ ] Implement a separate TLS-enabled `deploy/macos-production/` profile; do not expose the localhost test profile.
- [ ] Prove unattended Docker Desktop and Compose recovery after Mac reboot and managed updates.
- [ ] Validate the selected named-volume or APFS storage model at production capacity.
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
- [x] Test local-hit service during a simulated Jamf/JCDS outage.
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
| OQ-01 | Jamf file-resolution API contract | Blocking | RESOLVED | Use deprecated `GET /api/v1/jcds/files/{fileName}` and parse `uri` until Jamf introduces a replacement; map the observed 404 payload to not found | Monitor Jamf deprecation notices; capture remaining non-404 error responses for resilience tests |
| OQ-05 | Client and upstream range-request behavior | Blocking | OPEN | Capture real client traffic; on a miss fetch and publish the full object | `GET`, `HEAD`, resume and multi-range captures; JCDS response behavior |
| OQ-06 | Permitted JCDS hosts and redirects | Blocking | IN REVIEW | One exact CloudFront distribution class is observed; allow exact configured hostnames only and revalidate redirects | Additional production samples and redirect behavior; approved runtime hostname inventory |
| OQ-11 | Flat filenames or nested paths | Blocking | RESOLVED | V1 accepts exactly one filename segment ending in lowercase `.pkg`; nested paths and other types are out of scope | User-confirmed v1 scope; validation and negative tests |
| OQ-12 | Integrity source of truth | High | RESOLVED | Require exact catalog `length` and SHA3-512 match before atomic publication; MD5 is non-authoritative | Sanitized catalog fields captured; implementation and mismatch tests required |
| OQ-13 | JCDS catalog response shape | Blocking | RESOLVED | Parse the observed complete top-level JSON array; fail explicitly if a future response exposes an incomplete envelope | Complete response begins with `[` and has no pagination metadata in the observed contract |
| OQ-03 | Package workload and concurrency | High | IN REVIEW | Design for 500–2,000 Macs; measure package distribution and peak simultaneous fills before load-test targets are frozen | Package count/size distribution, largest package, and common simultaneous requests |
| OQ-07 | Store capacity and retention | High | IN REVIEW | Provision 500 GB–1 TB usable cache storage and retain 20% headroom; retention/eviction policy remains open | Inventory growth, reuse interval, and operational cleanup policy |
| OQ-02 | Client access control | High | RESOLVED | Server-authenticated TLS plus source CIDR `192.168.0.0/16`; no additional v1 authentication | Validate allowed and denied LAN paths at NGINX; OQ-18 determines feasibility under Docker Desktop |
| OQ-04 | Availability and service-level objective | Medium | OPEN | Provisional 99.5%, excluding approved maintenance, for the single-host release | Business impact, maintenance window and recovery expectations |
| OQ-08 | TLS certificate ownership | Medium | RESOLVED | Use `jcds-cache.appfruit.ch` and manual DNS validation for the pilot; certificate-expiry monitoring is mandatory | Assign the renewal owner and add unattended renewal before production approval |
| OQ-09 | Secret delivery platform | Medium | RESOLVED | Root-owned mode-`0600` host environment file passed to Docker; never place the value in Git, images or Compose YAML | Exercise secret rotation and accept/document Docker-administrator visibility |
| OQ-10 | Monitoring and alerting platform | Medium | OPEN | Use the existing enterprise platform and expose Prometheus-compatible metrics where supported | Platform, log format, metric scraping and alert ownership |
| OQ-14 | Production Mac hardware | Blocking | RESOLVED | Dedicated wired Mac mini, 24 GB RAM, 1 TB APFS | Confirm chip generation and usable capacity/headroom |
| OQ-15 | Docker Desktop licensing | Blocking | BLOCKED | Free tier requested but ineligible for enterprise production use | Obtain approved paid entitlement or change runtime architecture |
| OQ-16 | Unattended startup/session model | Blocking | IN REVIEW | Dedicated account; fully automatic recovery required | Startup mechanism plus cold-boot and update-recovery evidence |
| OQ-17 | Production storage backing | Blocking | RESOLVED | Docker named volume at `/srv/jamf-store`; administrative access from macOS is sufficient | Qualify sizing, atomicity, permissions, restart, reboot and recovery |
| OQ-18 | NGINX access enforcement/client IP | Blocking | IN REVIEW | No firewall; NGINX enforces `192.168.0.0/16` | Allowed/denied LAN test proving trustworthy source visibility |
| OQ-19 | Docker Desktop resources and updates | High | OPEN | Disable Resource Saver and manage fixed resources/maintenance windows | Managed settings and update ownership |
| OQ-20 | Cache backup/rebuild policy | Medium | OPEN | Rebuild package bytes from JCDS; protect configuration and certificates | Recovery owner and empty-cache recovery test |
| OQ-21 | Production helper UID model | Blocking | IN REVIEW | Prefer non-root; UID 0 only with explicit approval and constrained controls | Bind-mount permission test; exception only if required |

## 6. Definition of ready for coding

Repository scaffolding and mock-driven implementation can begin immediately. Real Jamf adapter completion is ready when:

- [x] OQ-01 has a sanitized successful and not-found contract plus an explicit deprecated-endpoint decision.
- [ ] OQ-05 has enough evidence to select the miss/range policy.
- [ ] OQ-06 has an enforceable destination and redirect allowlist.
- [x] OQ-11 fixes the canonical path model.
- [x] OQ-13 fixes the catalog response as a complete top-level JSON array.
- [x] The repository owner, name and public visibility are confirmed.
- [x] A GitHub connection with permission to create or write the repository is available.

Availability, retention and monitoring ownership remain open before production approval. TLS and secret-delivery decisions are fixed for the controlled pilot, while unattended certificate renewal remains an explicit gate.

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
| 2026-08-27 | Architecture | Selected NGINX plus a Go helper on one container host | Superseded on 2026-08-30 |
| 2026-08-27 | Storage | Selected a filename-preserving filesystem store with hidden temporary files and atomic publication | Resolved |
| 2026-08-27 | Package identity | Confirmed immutable filenames | Resolved |
| 2026-08-27 | Execution | Established contract validation, vertical slice, hardening, production integration and rollout phases | Active |
| 2026-08-27 | Repository | Confirmed public repository `fabianhartmann2/JCDS-ContentCache` and write access | Resolved |
| 2026-08-27 | Foundation | Published the Go/NGINX/Compose skeleton; initial GitHub CI passed | Complete |
| 2026-08-27 | Milestone M1 | Added automated deployed-path evidence for MISS-to-LOCAL delivery, local ranges, client-abort continuation, truncated-transfer cleanup and persistence across serving-container restarts | In review |
| 2026-08-27 | Phase 2 resilience | Added typed OAuth/Jamf/object failure categories, URL/body redaction tests, controlled downstream mappings and local-hit availability during an upstream outage | In review |
| 2026-08-27 | Host profile | Selected Ubuntu Server 26.04 LTS amd64 | Superseded on 2026-08-30 |
| 2026-08-27 | Access and secrets | Selected `192.168.0.0/16` CIDR-only client access and a root-owned Docker environment file for the Jamf secret | Resolved |
| 2026-08-27 | Production candidate | Added hardened Compose/NGINX configuration, manual-DNS certificate procedure, expiry validation, monitoring, update and rollback guidance | In review |
| 2026-08-30 | Production target | Replaced Ubuntu/Docker Engine with a dedicated Mac running Docker Desktop; retained the container application architecture and marked Mac operations decisions as blocking | Active |
| 2026-08-30 | Real backend | Demonstrated real OAuth/catalog/resolver/JCDS fill, integrity-validated publication and a byte-identical local hit with sanitized NGINX telemetry | Complete |

## 10. Immediate next actions

1. Implement the selected OQ-16 through OQ-18 and OQ-21 directions and collect their required recovery, bind-mount, LAN source-address and non-root evidence.
2. Implement a separate `deploy/macos-production/` profile with TLS, LAN exposure, managed storage and Mac-specific operational controls.
3. Capture actual managed-Mac `GET`, `HEAD`, resume and multi-range behavior from privacy-safe NGINX records to resolve OQ-05.
4. Confirm whether real resolver URLs redirect and complete the exact JCDS hostname inventory to resolve OQ-06.
5. Resolve Docker resource/update policy, backup/rebuild, certificate-renewal, retention, SLO and monitoring ownership before the controlled production pilot.
