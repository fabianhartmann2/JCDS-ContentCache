# macOS production deployment readiness

## Status and scope

The production target is a dedicated Mac running Docker Desktop. This document
defines the preparation and acceptance work for that target. The first
`deploy/macos-production/` profile now exists for controlled engineering
validation; it is not approved for a production pilot until the remaining
evidence and operational gates in this document are closed.

The existing profiles have different purposes:

| Profile | Purpose | Production status |
|---|---|---|
| `deploy/compose/` | Credential-free mock development and CI | Not production |
| `deploy/macos/` | Localhost-only real Jamf/JCDS integration test | Not production; never expose to the LAN |
| `deploy/production/` | Earlier Ubuntu/Docker Engine candidate | Superseded; retained temporarily for reference |
| `deploy/macos-production/` | TLS-enabled Mac production candidate | Implemented for controlled validation; not yet pilot-approved |

## Confirmed service boundary

| Setting | Confirmed value |
|---|---|
| Host platform | Dedicated Mac running Docker Desktop |
| Service DNS | `jcds-cache.appfruit.ch` |
| Listener | HTTPS on TCP 8443 |
| Client network | Any network with a route to the Mac listener |
| Client authentication | None; server-authenticated TLS only |
| Workload | 500–2,000 managed Macs |
| Cache capacity | Approximately 500–600 GB package working set with at least 30 percent package-store free space |
| Container data paths | `/srv/jamf-store/packages` and `/srv/jamf-store/.temporary` |
| Outbound network | Direct validated HTTPS; no proxy or TLS inspection |
| Package integrity | Exact Jamf catalog length and SHA3-512 before atomic publication |

## Production blockers

Production-pilot approval still requires closure of:

1. Mac model, Apple silicon generation, RAM, storage and network interface.
2. Paid Docker Desktop subscription ownership, renewal and support contacts; entitlement is available.
3. Dedicated macOS account and unattended restart/session model.
4. Named-volume capacity/recovery evidence and confirmation of the required macOS visibility semantics.
5. Production helper UID model for the selected storage implementation.

The pilot additionally requires Docker resource/update policy, cache recovery,
certificate-renewal ownership, monitoring ownership, retention and SLO.

## Recommended host baseline

The approved host baseline is:

- Dedicated wired Apple-silicon Mac mini.
- 24 GB RAM and 1 TB APFS storage.
- A Docker named volume in Docker Desktop's disk image, with at least 30 percent
  operational headroom; actual usable cache capacity must account for macOS,
  images, logs and temporary in-progress downloads.
- Static address or DHCP reservation and stable DNS/time synchronization.
- Supported macOS release managed through MDM.
- Sleep and automatic power-off disabled for the always-on service.
- UPS-backed power where required by the agreed recovery objective.

Docker Desktop supports the current and two previous major macOS releases.
The service owner must keep the host within that support window:

- <https://docs.docker.com/desktop/setup/install/mac-install/>

## Docker Desktop governance

An organization-approved paid Docker Desktop entitlement is available for this
production workload. Before pilot approval, record the subscription owner,
assigned Docker account or organization, renewal process and support contact.
Settings management and updates also need named owners:

- <https://docs.docker.com/subscription/desktop-license/>
- <https://docs.docker.com/enterprise/security/enforce-sign-in/>

The managed configuration must explicitly define:

- Docker Desktop version/update channel and maintenance window;
- CPU, memory, swap and disk-image limit;
- disk-image location;
- Resource Saver disabled;
- sign-in/organization enforcement where required;
- diagnostics and support-data handling;
- settings-change permissions for the dedicated service account.

Resource Saver is enabled by default and is unsuitable for an always-on cache:

- <https://docs.docker.com/desktop/use-desktop/resource-saver/>
- <https://docs.docker.com/desktop/settings-and-maintenance/settings/>

## Startup and session model

Docker Desktop is a macOS application backed by a Linux VM, not a conventional
host Docker Engine daemon. Container restart policies take effect only after
Docker Desktop is running. The approved operating model must document:

- the dedicated macOS account that owns/runs Docker Desktop;
- whether that account may remain logged in;
- how Docker Desktop starts after reboot;
- how the Compose application starts after Docker Desktop becomes ready;
- how startup failures alert operations;
- how FileVault unlock and unattended reboot constraints are handled;
- how macOS and Docker Desktop updates are staged and validated.

The production pilot must include cold-boot evidence from power-on to a healthy
HTTPS endpoint without undocumented manual actions. Docker Desktop provides a
CLI for start/stop/restart operations, but its use must be integrated into the
approved macOS session and management model:

- <https://docs.docker.com/desktop/features/desktop-cli/>

## Package-store selection

### Selected option — Docker named volume

Production uses a Docker named volume mounted at `/srv/jamf-store`. Docker
Desktop manages its files inside the Linux VM disk image, whose host location
resides on APFS storage. This is the model proven by the macOS real-backend
test and avoids the bind-mount I/O and cross-UID ownership failures already
observed during that test.

The package files are therefore not directly browsable in Finder. They can be
listed, inspected, exported, pre-populated and removed from macOS through
Docker commands or purpose-built administrative commands. The service owner
has confirmed that this satisfies the macOS visibility requirement; native
Finder visibility is not required.

The named volume is not approved for pilot use until testing proves:

- sustained large-file throughput;
- same-filesystem atomic rename;
- stable permissions across containers and reboots;
- correct Docker Desktop disk-image sizing and APFS free-space monitoring;
- behaviour across Docker Desktop and macOS updates;
- safe macOS-initiated inventory, pre-population, export and cleanup.

The qualification must use representative multi-gigabyte packages, concurrent
readers, an interrupted fill, container recreation, Docker Desktop restart,
macOS reboot and a controlled Docker Desktop update. It must also verify that
`.temporary` and `packages` reside in the same Docker volume and that only an
atomic rename makes a completed package visible.

The selected implementation must retain `/srv/jamf-store` as the internal
container root so application logic and tests remain portable.

## Cache backup and recovery

The recommended policy is to treat cached packages as derived data that can be
retrieved again from JCDS. Configuration, certificate material, deployment
revision, settings and runbooks require protection; package bytes do not require
backup unless operations chooses to preserve manually pre-populated content.

Recovery acceptance must demonstrate:

- rebuilding an empty package store;
- restoring configuration and certificates without exposing secrets;
- restarting after Docker Desktop VM-disk replacement;
- preserving or intentionally discarding completed packages according to the
  approved policy.

Docker's documented Desktop backup/restore procedure is a reference, not a
substitute for a tested service runbook:

- <https://docs.docker.com/desktop/settings-and-maintenance/backup-and-restore/>

## Secrets and certificates

The Jamf client secret must remain outside Git and outside container images. The
production decision is a protected Mac host file injected only into the helper.
The final path and ownership depend on the dedicated service-account model. The
file must not be placed inside the source checkout, copied to tickets or exposed
through normal logs.

TLS certificates for `jcds-cache.appfruit.ch` must be stored in a protected
macOS^v�kh��춻�q�^ted separately so local delivery can remain available during an upstream outage.

> **Priority: Must. Acceptance:** Container health checks and monitoring distinguish local failure from Jamf/JCDS unavailability.

### FR-021 Graceful shutdown

During an intentional restart, components must stop accepting new work, allow active transfers a configurable drain period, close streams cleanly and avoid publishing incomplete final files.

> **Priority: Must. Acceptance:** Restart testing during active downloads produces no corrupted final package files.

## 10. Non-functional requirements

### NFR-001 Availability

The production service must have an agreed availability target and maintenance window. The initial single-host design is a documented single point of failure; automatic container restart and host recovery reduce downtime but do not provide host-level high availability.

> **Priority: Must. Acceptance:** The approved service-level objective and single-host risk acceptance are recorded before go-live.

### NFR-002 Performance

A local hit should add minimal application overhead and use the available host/network throughput efficiently. Store-miss time to first byte must be dominated by Jamf resolution and upstream response rather than full-object buffering. Concrete throughput and concurrency targets must be set from workload data.

> **Priority: Must. Acceptance:** Load tests meet the agreed hit throughput, miss latency and concurrency targets.

### NFR-003 Scalability

The first release must support 500–2,000 managed Macs, the measured active-download concurrency, package-size distribution and an approximately 500–600 GB working set while retaining at least 30 percent package-store free space. The design should permit later migration to larger storage or a redundant deployment.

> **Priority: Must. Acceptance:** Capacity calculations and stress tests cover the agreed peak profile.

### NFR-004 Reliability

Processes must restart automatically after failure, final files in the persistent package store must survive container replacement and incomplete temporary files must be safely ignored or removed on recovery.

> **Priority: Must. Acceptance:** Process-kill and host-reboot tests preserve complete final packages and recover without manual repair.

### NFR-005 Transport security

All client and external upstream communication must use HTTPS with hostname verification and an approved trust chain. Disabling certificate verification is prohibited. Internal helper traffic must be confined to the host/container network; internal TLS may be added if required by platform policy.

> **Priority: Must. Acceptance:** TLS validation tests reject expired, untrusted and hostname-mismatched certificates.

### NFR-006 Least privilege

The Jamf API client must have only the permission required to read JCDS file download information. Containers must run without unnecessary Linux capabilities, with no-new-privileges and read-only filesystems except for explicitly writable paths.

> **Priority: Must. Acceptance:** Security review confirms API and runtime permissions.

### NFR-007 Secret protection

Client secrets, access tokens and signed URLs must not be embedded in images, source control, client-visible headers, metrics or standard logs. For v1, Docker must inject the Jamf client secret from a root-owned mode-`0600` host environment file outside the Git checkout. Rotation must require only an environment-file update and controlled helper replacement, not an image rebuild. Root and Docker administrators can inspect container environment values and are therefore trusted privileged operators.

> **Priority: Must. Acceptance:** Repository/image scans and log review find no secret material; rotation succeeds through the runbook.

### NFR-008 Egress restriction

Host and container egress should be restricted to the Jamf tenant, approved JCDS/CDN destinations and required enterprise infrastructure. DNS-based destinations and redirect changes must be handled without creating an arbitrary egress channel.

> **Priority: Must. Acceptance:** Firewall and application-policy tests block unapproved destinations.

### NFR-009 Auditability

The standard NGINX client-behavior log must contain timestamp, source client address, coarse client class, request/correlation ID, connection identifiers, HTTP protocol, method, range class, If-Range presence, result status, package source (LOCAL or JCDS), response range/length presence, byte count, duration, upstream status/timing and request completion. It must not contain the URI, package name, query string, raw Range or User-Agent values, Authorization, cookies, referrer, client tokens, Jamf tokens, client secrets or signed URLs. Source addresses are client-identifying operational data and require restricted access and an approved retention period.

> **Priority: Must. Acceptance:** Automated tests prove the behavior schema and representative GET, HEAD, start-at-zero, resumed, multi-range, local, upstream and error classifications; a disclosure check finds none of the excluded fields or values.

### NFR-010 Observability

Metrics must cover requests, local hits, store misses, bytes served, bytes downloaded, active fills, lock waits, helper latency, Jamf response categories, token refresh outcomes, final-store bytes/files, temporary bytes, cleanup actions, free disk space and failures.

> **Priority: Must. Acceptance:** Dashboards display the required metrics and test failures trigger the agreed alerts.

### NFR-011 Maintainability

Configuration must be externalized and validated at startup. Images and dependencies must be pinned, scanned and upgradeable independently. Jamf-specific code must be isolated behind an interface with contract tests.

> **Priority: Must. Acceptance:** A documented dependency update and adapter test can be completed without changing the downstream contract.

### NFR-012 Supportability

A production runbook must cover deployment, validation, log locations, health interpretation, package-store inspection, atomic pre-population, removal and cleanup, certificate and secret rotation, disk pressure, upstream outage, rollback and escalation ownership.

> **Priority: Must. Acceptance:** An operator unfamiliar with the implementation completes the standard recovery exercises using the runbook.

### NFR-013 Data minimization

The final package namespace must store raw package bytes only. Any minimum delivery metadata must remain outside the served namespace. OAuth responses and temporary signed URLs must not be persisted in the package store.

> **Priority: Must. Acceptance:** Filesystem inspection confirms human-readable package files and absence of token and signed-URL material.

### NFR-014 Compatibility

The downstream response must be compatible with the HTTP client used by the Mac software-installation workflow, including redirects, content length, byte ranges and timeout behaviour actually used by that client.

> **Priority: Must. Acceptance:** A representative managed Mac completes installation using only the package endpoint.

## 11. API and protocol contract

### 11.1 Client-facing contract

| **Element**                        | **Contract**                                                                     |
|------------------------------------|----------------------------------------------------------------------------------|
| Request                            | GET https://{service-host}:8443/packages/{filename}                              |
| Successful full response           | 200 OK with package body and accurate Content-Length where known                 |
| Successful range response          | 206 Partial Content with correct Content-Range after range behaviour is approved |
| Not found                          | 404 Not Found without exposing Jamf response details                             |
| Invalid request                    | 400 Bad Request or 404 according to the final security convention                |
| Dependency temporarily unavailable | 502 or 503 with a short non-sensitive response                                   |
| Dependency timeout                 | 504 Gateway Timeout or a closed partial stream if bytes were already sent        |
| Capacity failure                   | Controlled 5xx response; no final file is published                              |
| Diagnostic source header           | X-Package-Source: LOCAL/JCDS may be enabled for approved clients or diagnostics  |

### 11.2 Internal helper contract

- The helper receives only a canonical filename from NGINX, not an arbitrary URL.

- The helper returns the package stream and safe metadata on success.

- The helper coordinates one active upstream fill per canonical package path and gives waiters a completed local file or a controlled timeout.

- For an absent package, the helper requests a complete upstream object and does not forward a client Range header as an upstream partial-object request.

- The helper returns structured internal error categories that NGINX maps to the client contract.

- The helper exposes /livez, /readyz and /metrics only on the internal listener.

- The helper uses request correlation IDs but never includes signed URL query strings or Authorization values in logs.

- Maximum response header size, connection timeouts, redirect count, body size and supported package size are configurable and bounded.

### 11.3 OAuth behaviour

The token request uses the client-credentials grant and application/x-www-form-urlencoded fields:

- grant_type=client_credentials

- client_id from the mounted secret/configuration

- client_secret from the mounted secret

- scope only if explicitly required and approved for the configured Jamf API client

The helper must parse `access_token`, `token_type` and `expires_in` from a successful response, require a Bearer token type where supplied, tolerate the observed additional string `scope` field, and refresh with a safety margin. Because the observed lifetime is 59 seconds, a configured 60-second margin must be adaptively clamped; the baseline implementation uses no more than 20 percent of the returned lifetime so the token remains reusable while still refreshing early.

### 11.4 Filesystem mapping contract

| **Element**        | **Baseline mapping**                                                              |
|--------------------|-----------------------------------------------------------------------------------|
| Client request     | GET /packages/ExampleFile.pkg                                                     |
| Final local file   | /srv/jamf-store/packages/ExampleFile.pkg                                          |
| Temporary download | /srv/jamf-store/.temporary/{unique-id}.part                                       |
| Hit lookup         | NGINX try_files checks only the normalized final regular-file path                |
| Miss persistence   | The helper writes, verifies and atomically publishes from hidden temporary storage |

The paths above define the baseline layout and may be changed through configuration, but the one-to-one relationship between the canonical client path and the human-readable final filesystem path is mandatory.

## 12. Filesystem package-store design and data lifecycle

### 12.1 Storage model

- Use a dedicated persistent volume or filesystem rooted at a configurable path such as /srv/jamf-store.

- Store each completed package as raw bytes under the same normalized path and filename used by the client URL; do not wrap final files in NGINX cache metadata or hashed filenames.

- Place hidden temporary files on the same filesystem as final packages and outside the NGINX-served namespace so successful finalization can use an atomic rename.

- Serve only validated regular final files. Symbolic links, device files, sockets and files outside the configured root must never be followed or exposed.

- Keep configuration, TLS material, secrets, locks, operational metadata and logs outside the final package namespace.

- Reserve free capacity for at least the largest supported in-progress download plus operational headroom.

- Treat package-store contents as reconstructable derived data; back up configuration and runbooks, not necessarily stored packages.

### 12.2 Sizing method

> **Initial sizing formula:** Usable package-store capacity should be at least the expected active package working set plus concurrent temporary-download allowance, multiplied by 1.20 for operational headroom. The final value must be calculated from actual JCDS package inventory and download demand.

The first deployment targets an approximately 500–600 GB active package working set on the 1 TB Mac. At least 30 percent of the package-store filesystem must remain free. Package inventory, maximum object size and simultaneous-fill demand must still be measured before the Docker disk-image limit and load-test limits are frozen.

### 12.3 Retention and cleanup

Immutable filenames permit indefinite local reuse without HTTP freshness expiry or upstream revalidation. Final files remain available until an explicit administrative removal or a configured capacity-cleanup process selects them. Because the storage model is not NGINX proxy_cache, cleanup must be implemented and monitored separately, using recorded request activity or another approved inventory policy. Cleanup must skip active temporary files, locked paths and files below the configured minimum retention period.

### 12.4 Integrity

- Do not store Jamf API JSON, OAuth error bodies, CDN HTML errors, redirects without the final package, partial 206 responses or disallowed HTTP statuses as final package content.

- Retrieve the exact catalog entry for the requested immutable filename and reject missing, duplicate, malformed or incomplete-page metadata.

- Treat catalog `length` and SHA3-512 as the authoritative publication checks. Reject a conflicting upstream Content-Length before streaming when possible, count all received bytes and compute SHA3-512 incrementally while streaming.

- Preserve the full byte stream without transformation; compression and content rewriting must be disabled for package bodies.

- Publish only after the downloaded byte count and computed SHA3-512 match the catalog. MD5 is retained only for compatibility or diagnostics and is not the integrity security boundary.

- Client-side package signature verification remains part of the normal macOS installation trust chain and is not replaced by the local package store.

### 12.5 Pre-population and manual changes

- An operator may pre-populate an approved package by writing it to the non-public staging area, applying the required ownership and permissions, validating it, and atomically renaming it to the canonical final path.

- Direct writes into a publicly served final filename are prohibited because a client could observe a partial file.

- Existing final files must not be overwritten. A corrected package receives a new immutable filename; removal of an old file is a separate audited action.

- The service must detect and report unexpected files, unsafe permissions or symbolic links during operational validation.

## 13. Deployment and operations

### 13.1 Container deployment

| **Component**                   | **Purpose**                                                                                                                         | **Exposure**                                  |
|---------------------------------|-------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------|
| nginx                           | TLS listener on 8443, try_files lookup, static delivery, internal miss routing and downstream streaming                             | Client network + internal helper              |
| jamf-download-helper            | Single-flight coordination, OAuth, catalog/resolver adapters, URL policy, hashing, temporary files and atomic publication           | Internal container network only               |
| Persistent package-store volume | Human-readable final packages and hidden temporary files                                                                            | Writable only by the package-publication path |
| Secrets/certificates            | Jamf client secret in a root-owned host environment file; TLS key and trust configuration in the host certificate tree             | Helper environment only / NGINX read-only mount |

- Docker Desktop must be licensed for the enterprise, supported on the selected macOS release, managed through the approved Mac-management controls and assigned explicit CPU, memory and disk limits.

- Docker Desktop Resource Saver must be disabled for this always-on service. macOS and Docker Desktop updates must use controlled maintenance windows with post-update health validation.

- Images must use approved registries, pinned versions or digests and a documented patch cadence.

- The helper should be a small statically compiled service, such as Go, with bounded streaming buffers and no shell execution.

- The production helper should run as a non-root UID. If Docker Desktop's named-volume ownership model prevents this, UID 0 may be considered only through an approved exception with all capabilities dropped, `no-new-privileges`, a read-only root filesystem and the package volume as its only writable data path. NGINX may retain its standard root master solely to read the certificate and switch to unprivileged workers; it must drop all unrelated capabilities.

- Only the package-store staging/finalization path and explicitly required runtime directories may be writable; static-serving workers should have the minimum required write access.

- Startup must fail clearly on invalid configuration, missing secrets or invalid TLS material.

- Production readiness must demonstrate unattended recovery after a Mac reboot, Docker Desktop restart and managed update. The design must state whether a dedicated macOS account must remain logged in.

- The localhost-only `deploy/macos/` profile must not be exposed to the LAN. Production requires a separate TLS-enabled macOS Compose profile.

### 13.2 Configuration

| **Area**        | **Required external configuration**                                                                                               |
|-----------------|-----------------------------------------------------------------------------------------------------------------------------------|
| Service         | hostname, listen port, TLS certificate/key paths, client access policy                                                            |
| Jamf            | tenant base URL, OAuth path, catalog and resolver adapter versions, API client ID and secret reference                           |
| Upstream policy | allowed signed-URL host patterns, redirect limit, DNS/IP restrictions, TLS trust                                                  |
| Package store   | container root/temporary paths, Docker volume or APFS backing path, Docker disk-image location/limit, cleanup, lock timeout, minimum free space, ownership and permissions |
| HTTP            | connect/read/send timeouts, maximum package size, range policy, client-abort behaviour                                            |
| Observability   | log format/level, metrics listener, correlation header and monitoring destination                                                 |

### 13.3 Monitoring and alerting

The baseline NGINX behavior-log schema and privacy boundary are defined in `docs/client-request-monitoring.md`. Production collection may enrich records with deployment metadata, but it must not reintroduce the excluded URI, package identity, raw headers, credentials or signed URLs.

- Alert when the service or helper is not ready for longer than the agreed grace period.

- Alert on low package-store filesystem free space before active downloads are at risk.

- Alert on sustained Jamf authentication failures, resolver failures or signed-URL policy rejections.

- Alert on abnormal 5xx rate, incomplete transfers, unsafe filesystem objects, container restart loops or package-publication failures.

- Dashboard local hit ratio and bytes avoided, but interpret a low hit ratio against package release patterns rather than as an isolated fault.

- Retain sanitized access and service logs according to enterprise operational-log policy.

### 13.4 Recovery and continuity

- After container restart or host reboot, complete final files must remain usable and stale temporary files must be handled according to policy.

- Docker Desktop and the Compose application must recover without manual intervention after the approved macOS startup sequence. Container restart policies are not sufficient unless Docker Desktop itself starts successfully.

- If the package-store volume or Docker Desktop VM disk is lost, the service may start with an empty store and repopulate on demand after storage is restored. Configuration, certificates and deployment metadata must be recoverable independently of cached package bytes.

- If Jamf/JCDS is unavailable, locally stored immutable packages remain available; absent packages fail predictably.

- Rollback must restore the prior known-good images and configuration without deleting the package store unless incompatibility requires it.

- Credential compromise requires API client-secret rotation, process restart/reload, log review and revocation according to the security runbook.

## 14. Verification and acceptance

| **ID** | **Test**              | **Pass evidence**                                                                                                                                                                                |
|--------|-----------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AT-01  | Store miss            | Retrieve an absent package; verify one Jamf resolution, one JCDS transfer, MISS telemetry and publication under the exact canonical final filename.                                              |
| AT-02  | Streaming             | Throttle the upstream; verify the client receives bytes before upstream completion and memory remains bounded.                                                                                   |
| AT-03  | Local hit             | Repeat the request; verify identical bytes are served from the human-readable final path with LOCAL/HIT telemetry and no helper/Jamf call.                                                       |
| AT-04  | Concurrent miss       | Request one locally absent package from multiple clients; verify one upstream fill and correct waiting-client results.                                                                           |
| AT-05  | Client abort          | Disconnect the initiating client; verify the store fill completes, the final file is atomically published and the next request is a valid local HIT.                                             |
| AT-06  | Token reuse           | Request several locally absent packages within one token lifetime; verify a shared token.                                                                                                        |
| AT-07  | Token expiry/401      | Force expiration and 401; verify early renewal and at most one authenticated retry.                                                                                                              |
| AT-08  | Not found             | Request a filename absent from Jamf; verify a sanitized 404 and no final error-body file.                                                                                                        |
| AT-09  | Input attacks         | Test traversal, encoding, absolute URLs, control characters and oversized names; verify rejection without upstream calls.                                                                        |
| AT-10  | SSRF/redirect         | Return unsafe URLs and redirect chains from a mock resolver; verify every unsafe destination is blocked.                                                                                         |
| AT-11  | Transfer integrity    | Terminate a stream early and inject wrong length and SHA3-512 metadata; verify temporary state is discarded and no final file exists.                                                            |
| AT-12  | Range and HEAD        | Run the actual Mac client request patterns on local hits and store misses; verify static range responses and that a miss never publishes a partial 206 body as the final file.                   |
| AT-13  | Disk pressure         | Reach configured capacity and minimum-free-space thresholds; verify cleanup/alerts and controlled failure without deleting active fills.                                                         |
| AT-14  | Restart recovery      | Restart containers and reboot the host; verify complete entries survive and temporary state is handled safely.                                                                                   |
| AT-15  | Upstream outage       | Disable Jamf/JCDS; verify locally stored packages remain available and absent packages fail predictably.                                                                                         |
| AT-16  | TLS                   | Validate trusted access and rejection of expired, untrusted and hostname-mismatched certificates.                                                                                                |
| AT-17  | Secret redaction      | Inspect responses, logs, metrics, crash output, temporary files and the final package namespace for credentials, tokens and signed URLs.                                                         |
| AT-18  | Store administration  | Atomically pre-populate one validated package and verify a local HIT without Jamf access; remove it through the administrative procedure, verify the audit record and observe a subsequent MISS. |
| AT-19  | Load                  | Test agreed concurrent clients, largest package and hit throughput without resource exhaustion.                                                                                                  |
| AT-20  | Adapter compatibility | Run catalog and resolver contract tests against the selected deprecated responses and mocked future replacement adapters.                                                                        |

### 14.1 Production acceptance gates

- All Must requirements are implemented or have an approved exception with risk owner and expiry date.

- The exact Jamf endpoint, response schema, permissions and signed-URL destination policy are validated in the production tenant.

- Security review and threat-model actions are complete.

- Capacity and performance targets are approved and passed.

- Monitoring, alerting, ownership, on-call/escalation and runbooks are operational.

- Client compatibility is demonstrated on a representative managed Mac and installation workflow.

### 14.2 Current M1 automated evidence

The mock-driven milestone currently provides automated evidence for AT-01 through AT-05, the unsafe-redirect portion of AT-10, the truncation/length/digest portion of AT-11, the provisional miss/local-range portion of AT-12, serving-container persistence from AT-14, and dependency-independent local delivery from AT-15. The Docker Compose smoke test covers the deployed NGINX/helper path and verifies that upstream request counters remain unchanged for a repeated local hit, a local range request and a request after serving-container restart. It then stops the mock upstream, confirms the completed package remains locally available and verifies that an absent package receives a controlled error without creating a final file.

The helper also has automated status and redaction coverage for OAuth rejection/throttling/timeouts, Jamf `401` single-retry behavior, `403`, `429`, `5xx`, malformed responses, unsafe redirects and object failures. Client responses and diagnostic categories are tested independently from dependency response bodies, and transport failures are sanitized before a complete request URL could reach normal logs.

This evidence does not close the production gates. Actual managed-Mac traffic is still required for AT-12, actual JCDS destination and redirect observations are required for AT-10, and host reboot plus in-progress restart cases remain for AT-14.

- Rollback, secret rotation, certificate renewal and package-store-loss recovery are exercised.

## 15. Risks and mitigations

| **Risk**                     | **Impact**                                                                                                | **Primary mitigation**                                                                                                                                   |
|------------------------------|-----------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| Deprecated Jamf endpoints    | The selected resolver and catalog endpoints may be removed or changed before Jamf introduces replacements. | Accept and monitor the dependency risk; isolate both behind versioned adapters and migrate without changing client URLs when replacements appear.        |
| Mac/Docker Desktop outage    | Mac, user session, Docker Desktop VM or storage failure makes the service unavailable.                    | Prove unattended recovery, monitor every layer, document the single-node risk and define a later HA option if the SLO requires it.                       |
| Docker Desktop lifecycle     | Resource Saver, application updates, macOS updates or a missing user session can stop the service.        | Disable Resource Saver, manage settings and updates, use a dedicated operating model and test reboot/update recovery before pilot.                       |
| Docker VM disk exhaustion    | Named-volume growth can exhaust Docker's VM disk image or the underlying APFS volume.                     | Set explicit disk limits, monitor Docker and macOS free space, preserve 30 percent package-store free space and exercise cleanup/recovery.                |
| Signed-URL destination drift | JCDS/CDN hostnames may change and break a strict allowlist.                                               | Base the policy on Jamf-published domains, monitor rejections and use controlled configuration change rather than arbitrary egress.                      |
| Unsafe or partial-file publication | An interrupted transfer or tampered filesystem object could be exposed as a valid package. | Serve regular files only; deny symbolic links; keep temporary files outside the served namespace; publish only a complete validated 200 response by atomic rename; audit permissions and unexpected objects. |
| Range request bypass         | Clients that request only ranges may prevent full-object store population or cause duplicate traffic.     | Capture actual client behaviour; force a complete upstream retrieval on a miss and serve ranges from the completed local file.                           |
| Secret disclosure            | Tokens or signed URLs could appear in logs or diagnostics.                                                | Use structured redaction, avoid raw upstream URLs and Authorization headers, and include log inspection in acceptance.                                   |
| Disk exhaustion              | Large packages or concurrent fills could consume host storage.                                            | Dedicated volume, maximum-usage policy, minimum-free-space threshold, headroom, explicit cleanup and alerts.                                             |
| Filename reuse               | Replacing bytes under an existing name would make long-lived local files incorrect.                       | Enforce immutable/versioned naming and require explicit removal plus a new name for corrected content.                                                   |

## 16. Open questions and guided decisions

The following items are intentionally explicit. Recommended defaults permit detailed design to proceed, but the stated decision deadline shows when confirmation becomes mandatory.

#### OQ-01 — Jamf API contract (RESOLVED)

- **Observed evidence:** The deprecated `GET /api/v1/jcds/files/{fileName}` endpoint returns a successful JSON object whose signed download URL is in `uri`; an absent file returns HTTP 404 with `httpStatus` and an empty `errors` array.
- **Decision:** Use this deprecated endpoint until Jamf introduces a replacement. Keep it behind a configurable adapter, monitor Jamf deprecation notices and capture remaining non-404 error responses for resilience tests.
- **Required follow-up:** Migration trigger and runbook before production approval

#### OQ-02 — Client access control (RESOLVED)

- **Decision:** Use server-authenticated TLS without source-CIDR filtering or client authentication. Any network client able to route to TCP 8443 may request a known package filename and trigger an upstream fill.
- **Accepted consequence:** Transport is authenticated and encrypted, but clients are not authorized. Network exposure and package-name confidentiality are outside the application boundary.

#### OQ-03 — Workload and capacity (IN REVIEW)

- **Confirmed range:** The first release must support 500–2,000 managed Macs.
- **Decision still required:** What package count, total working-set bytes, maximum package size and peak simultaneous-fill count must be supported?
- **Recommended next step:** Measure recent inventory and demand, then load-test the active working set and concurrent temporary allowance with the 30 percent free-space floor.
- **Required by:** Detailed design and procurement

#### OQ-04 — Availability objective (OPEN)

- **Decision required:** What availability/SLO and recovery time are required for a production service?
- **Recommended default:** For the selected single-host first deployment, propose 99.5 percent excluding approved maintenance and document the single-host limitation; move to redundant hosts if a higher SLO is required.
- **Required by:** Production approval

#### OQ-05 — Range requests (OPEN)

- **Decision required:** Does the actual Mac installation client issue HEAD, Range from byte zero, resumed nonzero Range or multi-range requests?
- **Recommended default:** Capture requests from the real client. On a miss, retrieve and publish the complete object; determine whether the requesting client may receive a full 200 response or must wait for a correct 206 response from the completed file.
- **Required by:** Implementation design

#### OQ-06 — JCDS destination policy (IN REVIEW)

- **Observed evidence:** One sanitized resolver response used an HTTPS AWS CloudFront signed URL with time-limited signature query parameters.
- **Decision still required:** Which exact CloudFront hostnames and redirect patterns can legitimate temporary URLs use across the tenant's package inventory?
- **Recommended default:** Configure exact observed hostnames at deployment time, collect more sanitized samples, revalidate each redirect and reject wildcard CloudFront, non-HTTPS and private destinations.
- **Required by:** Security design

#### OQ-07 — Store retention and size (IMPLEMENTED; ACCEPTANCE PENDING)

- **Selected direction:** Plan approximately 500–600 GB usable cache on the 1 TB Mac. Default to a configurable 90-day inactivity window, trigger cleanup below 30 percent free space, delete oldest inactive completed packages first and stop at 35 percent free space.
- **Implementation:** A hardened `cache-maintainer` maintains a restricted last-access index from internal successful-request events and removes only validated regular final `.pkg` files. Filesystem `atime` is not authoritative. The helper rejects new fills below the same 30-percent floor.
- **Remaining acceptance:** Exercise controlled disk pressure and confirm index persistence, deletion order, restricted audit and clean refusal when no eligible file can restore the floor.
- **Required by:** Detailed design

#### OQ-08 — TLS ownership (RESOLVED FOR PILOT)

- **Decision:** Use `jcds-cache.appfruit.ch`; create DNS records manually and obtain the initial publicly trusted certificate through manual DNS validation.
- **Required follow-up:** Assign a certificate owner, alert at least 30 days before expiry and introduce unattended renewal before production approval. Manual renewal is acceptable only for the controlled pilot.

#### OQ-09 — Secret delivery (RESOLVED)

- **Decision:** Store the secret as an environment assignment in a root-owned mode-`0600` host file outside Git and let Docker inject it only into the helper container.
- **Required follow-up:** Exercise rotation through file replacement and helper recreation; restrict Docker administration because privileged operators can inspect container environment values.

#### OQ-10 — Monitoring platform (OPEN)

- **Decision required:** Which log, metric and alert platform will own the service telemetry?
- **Recommended default:** Integrate with the existing enterprise platform and expose Prometheus-compatible metrics if that matches current standards.
- **Required by:** Operations readiness

#### OQ-11 — Path model (RESOLVED)

- **Decision:** The first release accepts exactly one canonical filename segment ending in lowercase `.pkg`.
- **Excluded:** Nested paths, additional file types, uppercase `.PKG`, client-supplied URLs and query-selected destinations.
- **Required follow-up:** Retain positive and negative validation tests; any broader namespace requires an approved scope and threat-model update.

#### OQ-12 — Integrity metadata (RESOLVED)

- **Observed evidence:** Deprecated `GET /api/v1/jcds/files` returns `fileName`, `length`, `md5`, `region` and a 128-character hexadecimal `sha3` value for each package.
- **Decision:** Require exact `length` and SHA3-512 match before atomic publication. MD5 is non-authoritative and retained only for interoperability or diagnostics.
- **Required follow-up:** Production contract and checksum-mismatch acceptance test

#### OQ-13 — Catalog response shape (RESOLVED)

- **Observed evidence:** The complete `GET /api/v1/jcds/files` response begins with `[` and is a top-level JSON array of metadata entries without a pagination envelope.
- **Decision:** Parse the observed complete array. If a future response uses an envelope that reports more records than it contains, fail explicitly instead of returning a false not-found result.
- **Required follow-up:** Retain contract tests and monitor the deprecated endpoint for schema changes.

#### OQ-14 — Production Mac hardware (RESOLVED)

- **Decision:** Use a dedicated wired Mac mini with 24 GB RAM and 1 TB APFS storage.
- **Required follow-up:** Confirm the Apple silicon generation and prove that the selected Docker disk sizing supports the working set while retaining 30 percent package-store free space after images, logs and temporary downloads.

#### OQ-15 — Docker Desktop licensing (RESOLVED)

- **Decision:** Use Docker Desktop under the organization-approved paid entitlement now available for this production workload.
- **Required follow-up:** Record the subscription owner, assigned account/organization, renewal process and support contact. Update ownership remains under OQ-19.

#### OQ-16 — Unattended startup and session model (IN REVIEW)

- **Decision:** Use a dedicated macOS account. FileVault/login behavior is confirmed as handled. Use a managed per-user LaunchAgent—not a normal root LaunchDaemon—to start Docker Desktop in the GUI session, wait for `docker info`, reconcile Compose and validate trusted HTTPS readiness.
- **Required follow-up:** Implement the idempotent controller and demonstrate power-on-to-healthy recovery plus failure alerting without manual intervention. The live reboot test is explicitly deferred.

#### OQ-17 — Production storage backing and macOS visibility (RESOLVED)

- **Decision:** Use a Docker named volume at `/srv/jamf-store`, backed by Docker Desktop's disk image on managed APFS storage. Provide package inventory and management from macOS through Docker or purpose-built administrative commands.
- **Clarification:** Docker/administrative access from macOS satisfies the visibility requirement; native Finder browsing is not required.
- **Validated evidence:** Real fill, atomic publication, local byte identity, local range delivery and persistence across helper/NGINX restart passed on the target Mac.
- **Required follow-up:** Qualify disk sizing, atomic rename, permissions, interruption, restart, reboot, update and recovery; retain the 30 percent package-store free-space floor.

#### OQ-18 — NGINX access enforcement and client-address visibility (RESOLVED)

- **Observed evidence:** A LAN request from a managed client reached NGINX as Docker Desktop gateway `192.168.65.1`, not its real source address. Because the gateway itself matched the former `/16` allowlist, NGINX could not distinguish approved and unapproved clients.
- **Decision:** Remove source-CIDR filtering and continue without a host firewall or client authentication. Accept access by any client able to route to the listener.

#### OQ-19 — Docker Desktop resource and update policy (OPEN)

- **Owner decision:** The service owner will configure CPU, RAM, swap, disk-image limit, Resource Saver, automatic-update and maintenance-window settings.
- **Recommended default:** Disable Resource Saver, allocate fixed resources, prevent uncontrolled production updates and validate health after every macOS or Docker Desktop update.
- **Required by:** Operations readiness

#### OQ-20 — Cache recovery and backup (OPEN)

- **Selected direction:** Treat packages as rebuildable derived data without package-volume backup. Protect configuration, certificates, approved deployment revision and runbooks outside the volume.
- **Required follow-up:** Exercise deliberate empty-volume recovery: recreate the Compose volume and directory permissions, validate TLS, perform a real fill and repopulate on demand. Suspected corruption requires diagnostics and explicit approval before volume deletion.
- **Required by:** Recovery design

#### OQ-21 — Production helper identity (RESOLVED)

- **Decision:** Prefer a non-root helper. Permit UID 0 only after explicit approval, with `cap_drop: ALL`, `no-new-privileges`, a read-only root filesystem and only the package volume writable.
- **Implementation:** The macOS production profile uses UID `65532` with primary GID `0`; `store-init` creates group-writable named-volume directories without cross-UID `chown`. All helper capabilities remain dropped.
- **Validated evidence:** The target Mac started the helper healthy under this identity, completed a real JCDS fill, atomically published the package, survived serving-container restart and recovered after an intentional helper stop. No UID-0 exception is required.

### 16.1 Confirmed decisions

| **ID** | **Decision**           | **Confirmed value**                                                                                                                                             |
|--------|------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| D-01   | Delivery stage         | Production system.                                                                                                                                              |
| D-02   | Package identity       | Filenames are immutable; corrected versions receive new names.                                                                                                  |
| D-03   | Initial deployment     | NGINX and helper containers through Docker Desktop on one dedicated managed Mac.                                                                                |
| D-04   | Jamf API lifecycle     | Use the deprecated JCDS resolver and catalog endpoints until Jamf introduces replacements; keep both behind replaceable adapters.                                |
| D-05   | Publication integrity  | Require catalog length and SHA3-512 verification before atomic publication; do not treat MD5 as the security boundary.                                           |
| D-07   | Storage representation | Completed packages use a human-readable filesystem path matching the canonical client URL and original filename; opaque hashed proxy-cache storage is not used. |
| D-08   | V1 path scope          | Accept exactly one flat filename segment ending in lowercase `.pkg`; nested paths and other file types are excluded.                                                  |
| D-09   | Initial scale          | Design for 500–2,000 managed Macs and an approximately 500–600 GB package working set while preserving at least 30 percent package-store free space.                 |
| D-10   | Host profile           | Dedicated wired Mac mini with 24 GB RAM, 1 TB APFS storage, Docker Desktop and a dedicated service account; automatic recovery remains to be demonstrated. |
| D-15   | macOS storage          | Use a Docker named volume in Docker Desktop's APFS-backed VM disk image; Docker/administrative access from macOS satisfies the visibility requirement. |
| D-16   | Runtime identity       | Use validated UID `65532`, primary GID `0`, all capabilities dropped and a read-only root filesystem; no UID-0 exception is required. |
| D-11   | Service access         | Publish `jcds-cache.appfruit.ch:8443` with server-authenticated TLS and no source-CIDR filtering or client authentication. Any route-reachable client may request packages. |
| D-12   | Certificate            | Use manual DNS validation for the pilot with mandatory expiry alerting; unattended renewal remains a production gate.                                           |
| D-13   | Secret delivery        | Inject the Jamf client secret into the helper from a root-owned mode-`0600` host environment file outside Git.                                                   |
| D-14   | Outbound network       | Use direct validated HTTPS without an outbound proxy or TLS inspection.                                                                                          |

## 17. Delivery plan

| **Phase**                  | **Main activities**                                                                                                                       | **Exit evidence**                                              |
|----------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------|
| 1\. Contract validation    | Confirm Jamf API replacement/response, client HTTP behaviour, JCDS domains, API privilege and workload data.                              | Resolved OQ-01; confirmed population range; measured OQ-03/OQ-05/OQ-06 evidence; redacted API fixtures. |
| 2\. Detailed design        | Finalize component contract, filesystem-store/range strategy, cleanup policy, network policy, secrets, TLS, monitoring, capacity and SLO. | Approved technical and security design.                        |
| 3\. Build                  | Implement the Go helper, NGINX configuration, container images, deployment definition, metrics and runbook draft.                         | Versioned deployable artifacts and automated tests.            |
| 4\. Integration test       | Validate Jamf/JCDS interaction, streaming, filesystem mapping, local-store semantics, Mac client compatibility and failure handling.      | Acceptance evidence for functional requirements.               |
| 5\. Security and load test | Perform threat-model validation, secret/log review, SSRF tests, capacity and concurrency testing.                                         | Security approval and performance report.                      |
| 6\. Production rollout     | Deploy with controlled client scope, monitor results, validate support procedures and expand after the observation period.                | Production acceptance and service handover.                    |

## 18. Recommended next decision sequence

Resolve the remaining questions in this order because each answer constrains the next layer of design:

1.  Capture the remaining sanitized Jamf authentication, throttle and server-error shapes.

2.  Capture actual client GET/HEAD/Range behaviour and workload scale (OQ-03 and OQ-05).

3.  Confirm legitimate JCDS/CDN destinations and validate the resolved OQ-02 TLS-only service boundary (OQ-06).

4.  Set the SLO and cleanup policy, then close the startup, storage qualification, Docker policy and recovery evidence (OQ-04, OQ-07, OQ-16, OQ-17, OQ-19 and OQ-20).

5.  Assign certificate-renewal and monitoring ownership, exercise secret rotation, and close the operational follow-ups for OQ-08 to OQ-10.

6.  Retain regression coverage for the resolved path contract (OQ-11) and verify the resolved integrity policy (OQ-12) in production-like tests.

## Appendix A. Error handling matrix

| **Condition**                       | **Client outcome**  | **Store action**                                | **Diagnostic category** |
|-------------------------------------|---------------------|-------------------------------------------------|-------------------------|
| Invalid method/path/name            | 400/404/405         | Reject before helper                            | Sanitized client error  |
| Package absent in Jamf              | 404                 | Do not create a final file                      | resolver_not_found      |
| OAuth credentials rejected          | 502                 | Do not retry until credentials/configuration change | jamf_auth_failed     |
| Jamf API throttled                   | 503 + Retry-After   | Do not retry within the request                  | jamf_throttled          |
| Jamf resolver timeout               | 504                 | Bounded retry only if approved                  | jamf_resolver_timeout   |
| Jamf/JCDS unavailable or 5xx         | 502                 | Preserve local hits; fail an uncached request   | upstream_unavailable    |
| Malformed resolver JSON             | 502                 | Do not follow URL                               | jamf_response_invalid   |
| Catalog missing/duplicate/incomplete| 502/404 by condition | Do not resolve or download                     | jamf_catalog_invalid    |
| Signed URL rejected                 | 502                 | Do not connect                                  | download_url_rejected   |
| JCDS not found/expired URL          | 502/404 by policy   | May resolve one new URL and retry once          | jcds_download_failed    |
| Upstream transfer interrupted       | 5xx or stream reset | Discard or quarantine incomplete temporary file | upstream_incomplete     |
| Length or SHA3-512 mismatch          | Stream reset/logged failure | Discard temporary file; never publish final file | package_integrity_failed |
| Store write/disk full               | 507/5xx             | Do not publish final file; alert                | package_store_failed    |
| Local file available, upstream down | 200/206             | Serve immutable final file                      | local_hit               |

## Appendix B. Minimum telemetry

| **Area**      | **Minimum signals**                                                                                                |
|---------------|--------------------------------------------------------------------------------------------------------------------|
| Request       | request count, status, duration, bytes, method, sanitized package label                                            |
| Package store | LOCAL/JCDS source, hits, misses, final files/bytes, temporary bytes, lock wait, cleanup and administrative changes |
| Helper        | active streams, catalog/resolver latency and status, integrity failures, download latency/status, redirects rejected |
| OAuth         | refresh attempts, success/failure, token time-to-expiry; never token value                                         |
| Storage       | final bytes/files, free bytes/percent, temporary bytes, unsafe objects and publication failures                    |
| Runtime       | container health, restarts, CPU, memory, network and open connections                                              |

## Appendix C. Glossary

| **Term**                   | **Definition**                                                                                            |
|----------------------------|-----------------------------------------------------------------------------------------------------------|
| Local hit                  | A response served from an already-complete regular file in the local package store.                       |
| Package-store fill         | The upstream download and atomic publication that creates a new complete local package file.              |
| Store miss                 | A request for which no complete regular file exists at the canonical local path.                          |
| JCDS                       | Jamf Cloud Distribution Service.                                                                          |
| Pull-through package store | A human-readable local file store populated automatically when a client first requests an absent package. |
| Resolver                   | The Jamf API call that maps a package filename to a temporary download URL.                               |
| Signed URL                 | A time-limited URL carrying authorization data in its query parameters or signature.                      |
| Single-flight              | Coalescing simultaneous operations so one token refresh or object download serves multiple waiters.       |
| SSRF                       | Server-side request forgery: misuse of a server to connect to an attacker-selected destination.           |

## Appendix D. Authoritative references

[Jamf: Retrieve a download URL for a specific JCDS file](https://developer.jamf.com/jamf-pro/reference/get_v1-jcds-files-filename)

[Jamf: Retrieve a list of JCDS files and metadata](https://developer.jamf.com/jamf-pro/reference/get_v1-jcds-files)

[Jamf: Obtain an access token using an API Client](https://developer.jamf.com/jamf-pro/reference/postoauthtoken)

[Jamf: Client Credentials](https://developer.jamf.com/jamf-pro/docs/client-credentials)

[Jamf: Privileges and deprecations](https://developer.jamf.com/jamf-pro/docs/privileges-and-deprecations)

[Jamf: JCDS communication](https://learn.jamf.com/r/en-US/technical-articles/Jamf_Cloud_Distribution_Service_Communication)

[NGINX: try_files directive](https://nginx.org/en/docs/http/ngx_http_core_module.html#try_files)

*Reference status reviewed 27 August 2026. The target Jamf Pro tenant's own /api/doc remains authoritative for endpoint availability and schema at implementation time.*
