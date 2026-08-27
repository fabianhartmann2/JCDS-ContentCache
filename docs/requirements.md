# Jamf JCDS Filesystem-Backed Package Caching Server

*Technical project requirements — production requirements and technical specification*

**Status:** Draft for technical review

**Version:** 0.2

**Date:** 27 August 2026

**Owner:** Mac Workplace

**Target:** Production service on a single Linux container host

**Audience:** Mac Workplace engineering, infrastructure, security, operations and service ownership

> **Purpose:** Provide managed clients with a stable internal package URL while transparently retrieving absent packages from Jamf Cloud Distribution Service (JCDS), streaming them to the first requester and retaining complete packages under their original URL-derived filenames for subsequent local delivery.

## 1. Executive summary

The proposed service is a production filesystem-backed pull-through package store for software packages held in JCDS. Clients request a package through a company-controlled HTTPS endpoint such as https://packages.example.ch:8443/packages/ExampleFile.pkg. The canonical request path maps directly to a human-readable local path such as /srv/jamf-store/packages/ExampleFile.pkg. NGINX serves an existing complete file directly from that path. If the file is absent, NGINX forwards the request to an internal Jamf download helper, which obtains or reuses an OAuth access token, resolves a temporary JCDS download URL through the Jamf Pro API, and streams the package through NGINX. The response is forwarded to the first client while being written to hidden temporary storage; only a complete validated transfer is atomically published under the final original filename.

The service is deliberately split into two components. NGINX owns TLS termination, normalized filesystem lookup, static-file delivery, miss routing, downstream streaming and request controls. A small purpose-built helper owns OAuth client-credentials authentication, Jamf API interaction, JSON parsing, signed-URL validation, upstream streaming, temporary-file management, atomic publication and per-package single-flight coordination. This separation keeps lifecycle-sensitive Jamf API logic out of the web server configuration while preserving a simple local file layout.

> **Important dependency:** Jamf currently describes GET /api/v1/jcds/files/{fileName} as a deprecated endpoint. A sanitized tenant sample confirms that it returns the signed download URL in the JSON field `uri`. The implementation must confirm the supported replacement in the target Jamf Pro tenant and isolate Jamf-specific calls behind an adapter interface.

## 2. Business context and problem statement

Software packages are hosted in JCDS and can only be located through authenticated Jamf Pro API calls. Managed clients should not receive Jamf API credentials, OAuth tokens or temporary JCDS URLs. Repeated direct downloads consume external bandwidth and make package delivery dependent on WAN and upstream availability for every installation. A centrally managed filesystem-backed package store reduces repeated WAN transfer, provides a consistent internal URL, remains easy to inspect and pre-populate, and allows already-stored immutable packages to remain available during a temporary upstream outage.

### 2.1 Desired outcome

- A client can retrieve an approved package from one stable internal HTTPS namespace.

- The first request begins receiving data without waiting for the entire upstream object to be downloaded.

- Subsequent requests are served locally without a Jamf API call or JCDS download.

- Jamf credentials and temporary download URLs remain confined to the server-side helper.

- Concurrent requests for the same locally absent package do not create duplicate upstream transfers.

- The service is observable, supportable, recoverable and secure enough for production operation.

## 3. Objectives and success measures

### 3.1 Objectives

- Reduce repeated WAN and JCDS traffic by retaining successfully retrieved packages under their canonical filenames on local persistent storage.

- Present a predictable client interface independent of Jamf OAuth and temporary download-URL mechanics.

- Stream store-miss responses so client delivery starts while the upstream download is still active.

- Prevent credential disclosure and restrict the service to approved clients, package paths and upstream destinations.

- Provide clear operational telemetry for local-store effectiveness, capacity, upstream failures and authentication health.

- Allow the Jamf API integration to change without altering the client-facing package URLs.

### 3.2 Production success measures

| **Measure**     | **Production evidence**                                                                                                                                  |
|-----------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| Store behaviour | A validated first request is a MISS; the completed package appears at the deterministic local path, and a repeated request is a local HIT.               |
| Streaming       | The first client receives response bytes before the complete package has arrived at the package-store server.                                            |
| Coalescing      | A concurrency test produces only one upstream object download for one canonical package path.                                                            |
| Security        | No client ID, client secret, access token or signed JCDS URL appears in client responses or normal logs.                                                 |
| Integrity       | Only a complete successful object is atomically published as a reusable final file; interrupted or failed downloads remain outside the served namespace. |
| Operations      | Health, metrics, logs, package-store capacity and documented recovery procedures are available before production release.                                |

## 4. Scope

### 4.1 In scope

- HTTPS package retrieval through a controlled /packages/ URL namespace.

- NGINX filesystem lookup with try_files, static-file delivery, internal miss routing, streaming, TLS and request/access controls.

- OAuth 2.0 client-credentials token acquisition and in-memory token reuse.

- Jamf API resolution of a package filename to a temporary JCDS download URL.

- Secure following of the approved download URL and upstream redirects.

- A persistent human-readable package store using original filenames, a non-public temporary area, capacity protection, cleanup, pre-population and administrative removal.

- Containerized deployment on one managed Linux server.

- Logging, metrics, health checks, alert integration and operational documentation.

- Functional, security, resilience and performance validation for production acceptance.

### 4.2 Out of scope

- Uploading, replacing or deleting packages in JCDS.

- A general-purpose forward proxy or arbitrary external URL fetcher.

- Apple Content Caching or caching of Apple operating-system and App Store content.

- Package modification, signing, notarization or malware remediation.

- Software-installation orchestration on clients; the service only delivers bytes.

- A graphical package-store administration portal in the first release.

- Multi-region active-active service or horizontal scaling in the first deployment.

- Caching multiple Jamf tenants or unrelated upstream repositories unless added through a later approved scope change.

- Using NGINX's opaque hashed proxy_cache format as the authoritative package storage model.

## 5. Confirmed decisions and assumptions

| **Topic**              | **Decision or assumption**                                                                                                                                                 | **State**           |
|------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------|
| Delivery stage         | Production system                                                                                                                                                          | Resolved            |
| Package identity       | Package filenames are immutable; the same filename always represents the same bytes.                                                                                       | Resolved            |
| Initial deployment     | NGINX and the Jamf helper run as containers on one managed Linux host.                                                                                                     | Resolved            |
| Client namespace       | A stable HTTPS /packages/{filename} endpoint is used.                                                                                                                      | Baseline assumption |
| Upstream               | One Jamf Pro tenant and JCDS are used in the first release.                                                                                                                | Baseline assumption |
| Storage representation | A completed request path maps to the same human-readable local path and filename; for example, /packages/ExampleFile.pkg maps to /srv/jamf-store/packages/ExampleFile.pkg. | Resolved            |
| Store rebuild          | The package store is derived data and can be rebuilt from JCDS; configuration and secrets require backup, package contents do not.                                         | Baseline assumption |

> **Normative language:** Must indicates a mandatory production requirement. Should indicates a recommended requirement that may only be waived through a documented design decision. May identifies an optional capability.

## 6. Stakeholders and responsibilities

| **Stakeholder**               | **Primary responsibility**                                                                                       |
|-------------------------------|------------------------------------------------------------------------------------------------------------------|
| Service owner / Mac Workplace | Own requirements, client integration, service acceptance, package naming policy and support model.               |
| Platform / infrastructure     | Provide and patch the Linux host, container runtime, storage, network, DNS, TLS and monitoring integration.      |
| Security                      | Review authentication, secret storage, network controls, logging, image hardening and threat model.              |
| Jamf administration           | Create the least-privilege API role/client and confirm the supported JCDS download endpoint and response schema. |
| Operations                    | Monitor the service, respond to alerts, rotate secrets, manage capacity and follow recovery procedures.          |
| Managed clients               | Request packages using the published internal URL and supported HTTP behaviour.                                  |

## 7. Solution architecture

### 7.1 Logical components

**NGINX filesystem gateway:** Terminates downstream TLS; validates method and path; maps the canonical request URI to the final filesystem path; serves existing files; routes misses internally; streams responses; publishes successful full responses through proxy_store or an equivalent atomic writer; records delivery telemetry.

**Jamf download helper:** Exposes an internal-only package endpoint; coordinates one fill per canonical package path; manages OAuth tokens; calls the Jamf resolver API; parses JSON; validates the resulting URL; follows allowed redirects; streams the JCDS response.

**Persistent package-store volume:** Stores final package bytes under their original URL-derived names and keeps hidden temporary downloads on the same filesystem so publication can be atomic.

**Jamf OAuth endpoint:** Issues a short-lived access token using the configured client ID and client secret.

**Jamf JCDS resolver API:** Returns a temporary download URL for the requested package filename.

**JCDS/CDN object endpoint:** Provides the package bytes through the temporary, time-limited URL.

**Operations integration:** Collects logs, metrics and alerts and supplies secrets, certificates and configuration.

### 7.2 Trust boundaries

- Clients can reach only the NGINX HTTPS listener. The helper must not be exposed outside the container network or host loopback interface.

- NGINX does not receive the Jamf client secret. The helper receives secrets through the approved secret mechanism and retains access tokens in memory only.

- The helper may connect only to the configured Jamf tenant and approved JCDS/CDN destinations over validated TLS.

- A filename supplied by a client must never become an arbitrary upstream URL, filesystem path or shell input; traversal, encoded separators and symbolic-link escapes must be prevented.

### 7.3 Network flows

| **Source**      | **Destination**         | **Protocol**             | **Purpose**                                         |
|-----------------|-------------------------|--------------------------|-----------------------------------------------------|
| Managed client  | NGINX gateway           | TCP 8443 / HTTPS         | Retrieve packages                                   |
| NGINX gateway   | Jamf helper             | Container network / HTTP | Internal store-miss request                         |
| Jamf helper     | Jamf Pro tenant         | TCP 443 / HTTPS          | OAuth token and download-URL API                    |
| Jamf helper     | Approved JCDS/CDN hosts | TCP 443 / HTTPS          | Package download and allowed redirects              |
| Host/containers | Enterprise services     | As approved              | DNS, time, certificate validation, logs and metrics |

## 8. End-to-end behaviour

### 8.1 Local hit

1.  The client sends GET /packages/{filename} to the NGINX HTTPS endpoint.

2.  NGINX normalizes and validates the request and maps it to /srv/jamf-store/packages/{filename}.

3.  NGINX try_files finds a complete regular file at the final path and serves it directly from local storage.

4.  No OAuth request, Jamf resolver call or external package transfer occurs.

5.  The response is logged and counted as a local HIT.

### 8.2 Store miss

1.  NGINX finds no complete regular file at the final path and internally forwards the normalized filename to the Jamf helper.

2.  The helper or an equivalent coordinator acquires the single-flight lock for the canonical package path so only one upstream fill proceeds.

3.  The helper reuses a valid access token or obtains one with the OAuth client-credentials grant.

4.  The helper calls the supported Jamf JCDS resolver endpoint with Authorization: Bearer {token} and Accept: application/json.

5.  The helper validates the response, extracts the temporary URL and verifies its scheme, hostname, redirects and destination policy.

6.  The helper requests the complete package and streams response bytes to NGINX without buffering the complete object in application memory.

7.  NGINX streams received bytes to the first client while proxy_store or an equivalent writer saves them under the non-public temporary directory on the package-store filesystem.

8.  After a successful complete 200 response and transfer validation, the temporary file is atomically renamed to the exact final URL-derived path. A failure, partial response or truncation leaves no file in the served namespace.

9.  Waiting requests for the same canonical path are served from the completed final file.

### 8.3 Upstream outage

A complete immutable file in the local package store remains serviceable when Jamf or JCDS is unavailable. A package that is not stored locally cannot be resolved or downloaded and must return a controlled error. The service must not silently return a different version, partial data or an HTML/JSON error body with a successful package status.

## 9. Functional requirements

### FR-001 Client package endpoint

The service must expose an HTTPS endpoint in the form /packages/{filename} on the approved hostname and port. GET must be supported; HEAD and Range support are governed by FR-014.

> **Priority: Must. Acceptance:** A supported client retrieves an approved test package through the published URL.

### FR-002 Request validation

The gateway and helper must accept only the configured path namespace and a strictly validated, decoded filename. Traversal sequences, encoded separators, control characters, absolute URLs, query-driven upstream selection and malformed percent encoding must be rejected before any upstream call.

> **Priority: Must. Acceptance:** A negative test set returns a controlled 4xx response and causes no upstream request.

### FR-003 Deterministic filesystem mapping

The canonical package namespace and normalized filename must map directly and one-to-one to the final filesystem path. For the baseline root /srv/jamf-store, /packages/ExampleFile.pkg maps to /srv/jamf-store/packages/ExampleFile.pkg. Hashed or credential-dependent filenames must not be used for final package storage.

> **Priority: Must. Acceptance:** Equivalent valid requests map to one final file; the stored filename matches the request filename; distinct filenames never collide.

### FR-004 Local filesystem delivery

NGINX must use a regular-file existence check such as try_files and serve a complete matching final file directly from persistent local storage without invoking the helper.

> **Priority: Must. Acceptance:** Logs and metrics show a local HIT and zero helper/Jamf calls for a repeated or pre-populated file.

### FR-005 OAuth token acquisition

The helper must obtain an access token from the configured Jamf OAuth endpoint using grant_type=client_credentials, client_id and client_secret submitted as application/x-www-form-urlencoded data.

> **Priority: Must. Acceptance:** A valid API client obtains a token; invalid credentials produce a controlled dependency error without secret disclosure.

### FR-006 Token reuse and renewal

The helper must cache the access token in memory, honor the returned expiration and renew it before expiry. Concurrent requests must share one token-refresh operation. On a Jamf 401 response, the helper may invalidate the token and retry the resolver request exactly once with a newly acquired token.

> **Priority: Must. Acceptance:** Load and expiry tests show token reuse, single-flight renewal and no unbounded retry loop.

### FR-007 Jamf package resolution

For a store miss, the helper must call the supported Jamf API with the requested filename and Bearer token, require a successful JSON response and extract exactly one download URL using a versioned response adapter. The observed deprecated v1 contract uses the JSON field `uri`; the field must remain configurable for a future replacement contract.

> **Priority: Must. Acceptance:** Valid, not-found, malformed, unauthorized and deprecated-endpoint responses map to documented outcomes.

### FR-008 Temporary URL validation

Before downloading, the helper must require HTTPS, validate the hostname against an approved policy, reject embedded credentials and unsafe destinations, and revalidate every redirect. Signed URL query parameters must never be logged.

> **Priority: Must. Acceptance:** SSRF and redirect tests cannot reach unapproved, private, loopback or link-local destinations.

### FR-009 Streaming store fill

On a store miss, the helper and NGINX must stream the upstream body so the first client receives bytes before the complete package is downloaded. The helper must use bounded buffers and must not hold a package in memory.

> **Priority: Must. Acceptance:** A timed test demonstrates downstream bytes before upstream completion and stable memory usage during the largest supported package.

### FR-010 Complete-file publication

Only a complete successful full-package response with status 200 and valid transfer completion may become a reusable final file. The response must first be written under a non-public temporary path on the same filesystem and then atomically renamed to the exact final path. A 206 response, error body or incomplete transfer must never be published as the final package.

> **Priority: Must. Acceptance:** Interrupted, truncated, partial and failed transfers leave no regular file under the served final path.

### FR-011 Concurrent miss coalescing

Only one upstream fill operation should run for a canonical package path at a time. Other requests for that package must wait for the completed final file or receive a controlled timeout according to configuration.

> **Priority: Must. Acceptance:** A simultaneous-client test produces one resolver/download sequence and one final file for the package.

### FR-012 Client disconnect handling

The configured production behaviour must allow an already-started store fill to complete after the initiating client disconnects, subject to upstream timeout and shutdown controls.

> **Priority: Must. Acceptance:** A disconnected first client does not prevent a later client from receiving the completed local package.

### FR-013 Immutable package retention

Because filenames are immutable, a successful final file may be reused without upstream freshness revalidation until it is explicitly removed by the capacity or administrative procedure. A package must never be overwritten under the same filename.

> **Priority: Must. Acceptance:** Publishing and operational procedures enforce versioned names and demonstrate predictable repeat delivery.

### FR-014 HEAD and byte ranges

The service must define and test behaviour for HEAD and single byte-range requests on local hits and store misses. Local files should support normal static byte-range delivery. A miss must trigger a complete upstream 200 retrieval; a partial 206 response must never be published as the final file. Multi-range behaviour may be rejected if not required by clients.

> **Priority: Must. Acceptance:** The agreed client request patterns pass without corrupting the package store; local partial-content headers and lengths are correct.

### FR-015 Store capacity and cleanup

The package-store root, maximum usage, inactive-retention policy and minimum free-space protection must be configurable. Because this is a filesystem store rather than the NGINX proxy cache, an explicit cleanup process must remove selected inactive or least-recently-requested final files without affecting configuration, secrets or active temporary downloads.

> **Priority: Must. Acceptance:** Capacity testing shows controlled cleanup and no host filesystem exhaustion.

### FR-016 Administrative store management

An authenticated local-administration procedure must allow an operator to inspect, atomically pre-populate and remove one named final package and optionally clear all derived packages. Pre-population must use the same validation, ownership and no-overwrite rules as an automatic fill. Actions must be explicit, logged and unavailable to ordinary clients.

> **Priority: Must. Acceptance:** An operator pre-populates a test package and receives a local HIT without Jamf access, then removes it and observes a MISS on the next request.

### FR-017 Controlled error mapping

The service must distinguish invalid client requests, package not found, Jamf authentication failure, resolver failure, signed-URL rejection, upstream timeout, upstream transfer failure and local capacity failure. JSON or HTML API errors must not be returned as successful package files.

> **Priority: Must. Acceptance:** Each injected fault returns the documented status or connection failure and records a sanitized diagnostic.

### FR-018 Dependency-independent client URL

Jamf endpoint versions, tokens and temporary URL formats must not appear in the client-facing URL. Replacing the Jamf adapter must not require changing existing package URLs.

> **Priority: Must. Acceptance:** A mocked adapter version change passes the same client contract tests.

### FR-019 Content metadata and static headers

On a miss response, the service should preserve safe and meaningful package headers. On a local hit, NGINX must derive accurate Content-Length and range behaviour from the final file and may generate local ETag and Last-Modified values. Authentication headers, cookies, resolver JSON and temporary-URL metadata must never be stored or returned.

> **Priority: Must. Acceptance:** Header comparison confirms correct client behaviour without leaking upstream secrets or implementation details.

### FR-020 Health and diagnostics endpoints

The gateway and helper must provide internal health endpoints. Liveness must reflect process health; readiness must reflect configuration and ability to accept requests. External dependency health must be reported separately so local delivery can remain available during an upstream outage.

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

The first release must support the agreed managed-client population, active-download concurrency, package size and working-set capacity with at least 20 percent storage headroom. The design should permit later migration to larger storage or a redundant deployment.

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

Client secrets, access tokens and signed URLs must not be embedded in images, source control, client-visible headers, metrics or standard logs. Secrets must be mounted or retrieved through the enterprise-approved secret mechanism and rotation must not require rebuilding images.

> **Priority: Must. Acceptance:** Repository/image scans and log review find no secret material; rotation succeeds through the runbook.

### NFR-008 Egress restriction

Host and container egress should be restricted to the Jamf tenant, approved JCDS/CDN destinations and required enterprise infrastructure. DNS-based destinations and redirect changes must be handled without creating an arbitrary egress channel.

> **Priority: Must. Acceptance:** Firewall and application-policy tests block unapproved destinations.

### NFR-009 Auditability

Operational logs must contain timestamp, request/correlation ID, sanitized package name, result status, package source (LOCAL or JCDS), byte count, duration and high-level error category. Client tokens, Jamf tokens, client secrets and signed query strings must never be logged.

> **Priority: Must. Acceptance:** A reviewed log sample supports incident diagnosis without sensitive fields.

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

The helper must parse access_token, token_type and expires_in from a successful response, require a Bearer token type where supplied, and refresh with a safety margin. A recommended starting margin is 60 seconds, subject to the returned lifetime and testing.

### 11.4 Filesystem mapping contract

| **Element**        | **Baseline mapping**                                                              |
|--------------------|-----------------------------------------------------------------------------------|
| Client request     | GET /packages/ExampleFile.pkg                                                     |
| Final local file   | /srv/jamf-store/packages/ExampleFile.pkg                                          |
| Temporary download | /srv/jamf-store/.temporary/{unique-id}.part                                       |
| Hit lookup         | NGINX try_files checks only the normalized final regular-file path                |
| Miss persistence   | NGINX proxy_store or an equivalent writer publishes only a complete full response |

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

A provisional 500 GB package store may be used for design validation, but it is not a committed production requirement until package inventory, maximum object size and demand are measured.

### 12.3 Retention and cleanup

Immutable filenames permit indefinite local reuse without HTTP freshness expiry or upstream revalidation. Final files remain available until an explicit administrative removal or a configured capacity-cleanup process selects them. Because the storage model is not NGINX proxy_cache, cleanup must be implemented and monitored separately, using recorded request activity or another approved inventory policy. Cleanup must skip active temporary files, locked paths and files below the configured minimum retention period.

### 12.4 Integrity

- Do not store Jamf API JSON, OAuth error bodies, CDN HTML errors, redirects without the final package, partial 206 responses or disallowed HTTP statuses as final package content.

- Validate Content-Length completion when provided and treat premature EOF as failure.

- Preserve the full byte stream without transformation; compression and content rewriting must be disabled for package bodies.

- Where Jamf exposes a trusted size or checksum, compare it before publishing the final file. This is an enhancement unless the exact metadata is confirmed.

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
| nginx                           | TLS listener on 8443, try_files lookup, static delivery, internal miss routing, streaming and proxy_store or equivalent persistence | Client network + internal helper              |
| jamf-download-helper            | Single-flight coordination, OAuth, Jamf resolver adapter, URL policy and JCDS streaming                                             | Internal container network only               |
| Persistent package-store volume | Human-readable final packages and hidden temporary files                                                                            | Writable only by the package-publication path |
| Secrets/certificates            | Jamf client secret, TLS key and trust configuration                                                                                 | Read-only mounts or approved secret provider  |

- Images must use approved registries, pinned versions or digests and a documented patch cadence.

- The helper should be a small statically compiled service, such as Go, with bounded streaming buffers and no shell execution.

- Containers should run as non-root on port 8443, drop unused capabilities, use no-new-privileges and have read-only root filesystems.

- Only the package-store staging/finalization path and explicitly required runtime directories may be writable; static-serving workers should have the minimum required write access.

- Startup must fail clearly on invalid configuration, missing secrets or invalid TLS material.

### 13.2 Configuration

| **Area**        | **Required external configuration**                                                                                               |
|-----------------|-----------------------------------------------------------------------------------------------------------------------------------|
| Service         | hostname, listen port, TLS certificate/key paths, client access policy                                                            |
| Jamf            | tenant base URL, OAuth path, resolver adapter/version, API client ID and secret reference                                         |
| Upstream policy | allowed signed-URL host patterns, redirect limit, DNS/IP restrictions, TLS trust                                                  |
| Package store   | root, temporary path, maximum usage, cleanup/inactive retention, lock timeout, minimum free space, file ownership and permissions |
| HTTP            | connect/read/send timeouts, maximum package size, range policy, client-abort behaviour                                            |
| Observability   | log format/level, metrics listener, correlation header and monitoring destination                                                 |

### 13.3 Monitoring and alerting

- Alert when the service or helper is not ready for longer than the agreed grace period.

- Alert on low package-store filesystem free space before active downloads are at risk.

- Alert on sustained Jamf authentication failures, resolver failures or signed-URL policy rejections.

- Alert on abnormal 5xx rate, incomplete transfers, unsafe filesystem objects, container restart loops or package-publication failures.

- Dashboard local hit ratio and bytes avoided, but interpret a low hit ratio against package release patterns rather than as an isolated fault.

- Retain sanitized access and service logs according to enterprise operational-log policy.

### 13.4 Recovery and continuity

- After container restart or host reboot, complete final files must remain usable and stale temporary files must be handled according to policy.

- If the package-store volume is lost, the service may start with an empty store and repopulate on demand after storage is restored.

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
| AT-11  | Truncated upstream    | Terminate the JCDS stream early; verify the client fails, temporary state is discarded or quarantined and no final file exists.                                                                  |
| AT-12  | Range and HEAD        | Run the actual Mac client request patterns on local hits and store misses; verify static range responses and that a miss never publishes a partial 206 body as the final file.                   |
| AT-13  | Disk pressure         | Reach configured capacity and minimum-free-space thresholds; verify cleanup/alerts and controlled failure without deleting active fills.                                                         |
| AT-14  | Restart recovery      | Restart containers and reboot the host; verify complete entries survive and temporary state is handled safely.                                                                                   |
| AT-15  | Upstream outage       | Disable Jamf/JCDS; verify locally stored packages remain available and absent packages fail predictably.                                                                                         |
| AT-16  | TLS                   | Validate trusted access and rejection of expired, untrusted and hostname-mismatched certificates.                                                                                                |
| AT-17  | Secret redaction      | Inspect responses, logs, metrics, crash output, temporary files and the final package namespace for credentials, tokens and signed URLs.                                                         |
| AT-18  | Store administration  | Atomically pre-populate one validated package and verify a local HIT without Jamf access; remove it through the administrative procedure, verify the audit record and observe a subsequent MISS. |
| AT-19  | Load                  | Test agreed concurrent clients, largest package and hit throughput without resource exhaustion.                                                                                                  |
| AT-20  | Adapter compatibility | Run resolver contract tests against the supported Jamf API response and a mocked future adapter version.                                                                                         |

### 14.1 Production acceptance gates

- All Must requirements are implemented or have an approved exception with risk owner and expiry date.

- The exact Jamf endpoint, response schema, permissions and signed-URL destination policy are validated in the production tenant.

- Security review and threat-model actions are complete.

- Capacity and performance targets are approved and passed.

- Monitoring, alerting, ownership, on-call/escalation and runbooks are operational.

- Client compatibility is demonstrated on a representative managed Mac and installation workflow.

- Rollback, secret rotation, certificate renewal and package-store-loss recovery are exercised.

## 15. Risks and mitigations

| **Risk**                     | **Impact**                                                                                                | **Primary mitigation**                                                                                                                                   |
|------------------------------|-----------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| Deprecated Jamf endpoint     | The referenced resolver endpoint may be removed or changed.                                               | Confirm the supported replacement before build; isolate it behind a versioned adapter and contract tests.                                                |
| Single-host outage           | Host or storage failure makes the service unavailable.                                                    | Document and accept the initial risk; automate restart/rebuild and define a later HA option if the SLO requires it.                                      |
| Signed-URL destination drift | JCDS/CDN hostnames may change and break a strict allowlist.                                               | Base the policy on Jamf-published domains, monitor rejections and use controlled configuration change rather than arbitrary egress.                      |
| Unsafe or partial-file publication | An interrupted transfer or tampered filesystem object could be exposed as a valid package. | Serve regular files only; deny symbolic links; keep temporary files outside the served namespace; publish only a complete validated 200 response by atomic rename; audit permissions and unexpected objects. |
| Range request bypass         | Clients that request only ranges may prevent full-object store population or cause duplicate traffic.     | Capture actual client behaviour; force a complete upstream retrieval on a miss and serve ranges from the completed local file.                           |
| Secret disclosure            | Tokens or signed URLs could appear in logs or diagnostics.                                                | Use structured redaction, avoid raw upstream URLs and Authorization headers, and include log inspection in acceptance.                                   |
| Disk exhaustion              | Large packages or concurrent fills could consume host storage.                                            | Dedicated volume, maximum-usage policy, minimum-free-space threshold, headroom, explicit cleanup and alerts.                                             |
| Filename reuse               | Replacing bytes under an existing name would make long-lived local files incorrect.                       | Enforce immutable/versioned naming and require explicit removal plus a new name for corrected content.                                                   |

## 16. Open questions and guided decisions

The following items are intentionally explicit. Recommended defaults permit detailed design to proceed, but the stated decision deadline shows when confirmation becomes mandatory.

#### OQ-01 — Jamf API contract (IN REVIEW)

- **Observed evidence:** The deprecated `GET /api/v1/jcds/files/{fileName}` endpoint returned a successful JSON object whose signed download URL is in `uri`.
- **Decision still required:** Which supported replacement endpoint and response field supersede this contract in the target tenant?
- **Recommended default:** Keep the observed contract behind the configurable adapter, validate the tenant's `/api/doc`, and capture sanitized error responses before architecture sign-off.
- **Required by:** Architecture sign-off

#### OQ-02 — Client access control (OPEN)

- **Decision required:** Are clients authorized by network location, mutual TLS, another identity mechanism, or a combination?
- **Recommended default:** Use server-authenticated TLS plus enterprise network allowlisting for the first internal deployment; add mTLS if clients can reach the service from untrusted networks.
- **Required by:** Security design

#### OQ-03 — Workload and capacity (OPEN)

- **Decision required:** How many Macs, concurrent downloads, packages, total working-set bytes and maximum package size must be supported?
- **Recommended default:** Measure recent inventory and demand; size the package store for the active working set, concurrent temporary files and 20 percent headroom.
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

#### OQ-07 — Store retention and size (OPEN)

- **Decision required:** What maximum package-store usage, minimum retention period and cleanup selection policy should be configured?
- **Recommended default:** Start design validation at 500 GB and 180 days minimum inactivity before cleanup; replace these with measured values before go-live. No freshness expiry is required for immutable names.
- **Required by:** Detailed design

#### OQ-08 — TLS ownership (OPEN)

- **Decision required:** Which DNS name, certificate authority and renewal mechanism will supply the client-facing certificate?
- **Recommended default:** Use an enterprise DNS name and automatically renewed enterprise PKI certificate; avoid manual certificate replacement.
- **Required by:** Infrastructure build

#### OQ-09 — Secrets platform (OPEN)

- **Decision required:** Where will the Jamf client secret be stored and how will rotation be delivered to the helper?
- **Recommended default:** Use the existing enterprise secret store or container-secret mechanism with read-only file delivery and restart/reload after rotation.
- **Required by:** Security and operations design

#### OQ-10 — Monitoring platform (OPEN)

- **Decision required:** Which log, metric and alert platform will own the service telemetry?
- **Recommended default:** Integrate with the existing enterprise platform and expose Prometheus-compatible metrics if that matches current standards.
- **Required by:** Operations readiness

#### OQ-11 — Path model (OPEN)

- **Decision required:** Will the namespace contain filenames only, or must it support nested subpaths and additional file types?
- **Recommended default:** Limit the first release to one canonical filename segment ending in `.pkg`; add other types or nested paths only with explicit validation and filesystem-mapping rules.
- **Required by:** API contract freeze

#### OQ-12 — Integrity metadata (OPEN)

- **Decision required:** Does Jamf expose an authoritative size or checksum that can be verified before final-file publication?
- **Recommended default:** Preserve transport length and rely on normal signed-package validation initially; add checksum verification when trusted metadata is available.
- **Required by:** Implementation design

### 16.1 Confirmed decisions

| **ID** | **Decision**           | **Confirmed value**                                                                                                                                             |
|--------|------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| D-01   | Delivery stage         | Production system.                                                                                                                                              |
| D-02   | Package identity       | Filenames are immutable; corrected versions receive new names.                                                                                                  |
| D-03   | Initial deployment     | NGINX and helper containers on one managed Linux host.                                                                                                          |
| D-04   | Storage representation | Completed packages use a human-readable filesystem path matching the canonical client URL and original filename; opaque hashed proxy-cache storage is not used. |

## 17. Delivery plan

| **Phase**                  | **Main activities**                                                                                                                       | **Exit evidence**                                              |
|----------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------|
| 1\. Contract validation    | Confirm Jamf API replacement/response, client HTTP behaviour, JCDS domains, API privilege and workload data.                              | Resolved OQ-01, OQ-03, OQ-05 and OQ-06; redacted API fixtures. |
| 2\. Detailed design        | Finalize component contract, filesystem-store/range strategy, cleanup policy, network policy, secrets, TLS, monitoring, capacity and SLO. | Approved technical and security design.                        |
| 3\. Build                  | Implement the Go helper, NGINX configuration, container images, deployment definition, metrics and runbook draft.                         | Versioned deployable artifacts and automated tests.            |
| 4\. Integration test       | Validate Jamf/JCDS interaction, streaming, filesystem mapping, local-store semantics, Mac client compatibility and failure handling.      | Acceptance evidence for functional requirements.               |
| 5\. Security and load test | Perform threat-model validation, secret/log review, SSRF tests, capacity and concurrency testing.                                         | Security approval and performance report.                      |
| 6\. Production rollout     | Deploy with controlled client scope, monitor results, validate support procedures and expand after the observation period.                | Production acceptance and service handover.                    |

## 18. Recommended next decision sequence

Resolve the remaining questions in this order because each answer constrains the next layer of design:

1.  Capture the supported Jamf API contract and a redacted example response (OQ-01).

2.  Capture actual client GET/HEAD/Range behaviour and workload scale (OQ-03 and OQ-05).

3.  Confirm legitimate JCDS/CDN destinations and downstream access control (OQ-02 and OQ-06).

4.  Set the SLO, package-store capacity, cleanup policy and Linux host/storage sizing (OQ-04 and OQ-07).

5.  Assign DNS/TLS, secrets, monitoring and operational ownership (OQ-08 to OQ-10).

6.  Freeze the client path contract and integrity controls (OQ-11 and OQ-12).

## Appendix A. Error handling matrix

| **Condition**                       | **Client outcome**  | **Store action**                                | **Diagnostic category** |
|-------------------------------------|---------------------|-------------------------------------------------|-------------------------|
| Invalid method/path/name            | 400/404/405         | Reject before helper                            | Sanitized client error  |
| Package absent in Jamf              | 404                 | Do not create a final file                      | resolver_not_found      |
| OAuth credentials rejected          | 502/503             | Retry only according to token policy            | jamf_auth_failed        |
| Jamf resolver timeout               | 504                 | Bounded retry only if approved                  | jamf_resolver_timeout   |
| Malformed resolver JSON             | 502                 | Do not follow URL                               | jamf_response_invalid   |
| Signed URL rejected                 | 502                 | Do not connect                                  | download_url_rejected   |
| JCDS not found/expired URL          | 502/404 by policy   | May resolve one new URL and retry once          | jcds_download_failed    |
| Upstream transfer interrupted       | 5xx or stream reset | Discard or quarantine incomplete temporary file | upstream_incomplete     |
| Store write/disk full               | 507/5xx             | Do not publish final file; alert                | package_store_failed    |
| Local file available, upstream down | 200/206             | Serve immutable final file                      | local_hit               |

## Appendix B. Minimum telemetry

| **Area**      | **Minimum signals**                                                                                                |
|---------------|--------------------------------------------------------------------------------------------------------------------|
| Request       | request count, status, duration, bytes, method, sanitized package label                                            |
| Package store | LOCAL/JCDS source, hits, misses, final files/bytes, temporary bytes, lock wait, cleanup and administrative changes |
| Helper        | active streams, resolver latency/status, download latency/status, redirects rejected                               |
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

[Jamf: Obtain an access token using an API Client](https://developer.jamf.com/jamf-pro/reference/postoauthtoken)

[Jamf: Client Credentials](https://developer.jamf.com/jamf-pro/docs/client-credentials)

[Jamf: JCDS communication](https://learn.jamf.com/r/en-US/technical-articles/Jamf_Cloud_Distribution_Service_Communication)

[NGINX: try_files directive](https://nginx.org/en/docs/http/ngx_http_core_module.html#try_files)

[NGINX: proxy_store directive](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_store)

[NGINX: proxy_store and full 200 responses](https://trac.nginx.org/nginx/ticket/258)

*Reference status reviewed 27 August 2026. The target Jamf Pro tenant's own /api/doc remains authoritative for endpoint availability and schema at implementation time.*
