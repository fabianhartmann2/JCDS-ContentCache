# Jamf JCDS Filesystem-Backed Package Caching Server

*Technical project requirements â€” production requirements and technical specification*

**Status:** Draft for technical review

**Version:** 0.7

**Date:** 30 August 2026

**Owner:** Mac Workplace

**Target:** Production service on a dedicated Mac running Docker Desktop

**Audience:** Mac Workplace engineering, infrastructure, security, operations and service ownership

> **Purpose:** Provide managed clients with a stable internal package URL while transparently retrieving absent packages from Jamf Cloud Distribution Service (JCDS), streaming them to the first requester and retaining complete packages under their original URL-derived filenames for subsequent local delivery.

## 1. Executive summary

The proposed service is a production filesystem-backed pull-through package store for software packages held in JCDS. Clients request a package through the company-controlled endpoint `https://jcds-cache.appfruit.ch:8443/packages/ExampleFile.pkg`. NGINX and a Go helper run as containers inside Docker Desktop's Linux VM on a dedicated Mac. The canonical request path maps directly to a human-readable container path such as `/srv/jamf-store/packages/ExampleFile.pkg`, backed by persistent Docker Desktop storage. NGINX serves an existing complete file directly from that path. If the file is absent, NGINX forwards the request to the helper, which obtains or reuses an OAuth access token, reads authoritative size and SHA3-512 metadata, resolves a temporary JCDS download URL through the Jamf Pro API, and streams the package through NGINX. The response is forwarded to the first client while being written and hashed in hidden temporary storage; only a transfer matching the Jamf catalog length and SHA3-512 digest is atomically published under the final original filename.

The service is deliberately split into two components. NGINX owns TLS termination, normalized filesystem lookup, static-file delivery, miss routing, downstream streaming and request controls. A small purpose-built helper owns OAuth client-credentials authentication, Jamf API interaction, JSON parsing, signed-URL validation, upstream streaming, temporary-file management, atomic publication and per-package single-flight coordination. This separation keeps lifecycle-sensitive Jamf API logic out of the web server configuration while preserving a simple local file layout.

> **Accepted dependency risk:** Jamf marks `GET /api/v1/jcds/files/{fileName}` and `GET /api/v1/jcds/files` as deprecated, and no replacement is currently available for this tenant. The first release will use these endpoints behind replaceable adapters, parse the resolver field `uri`, and verify the file-list fields `length` and `sha3`. The service owner must monitor Jamf deprecation notices and migrate when a replacement becomes available.

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

- Jamf API lookup of authoritative package length and SHA3-512 metadata before publication.

- Secure following of the approved download URL and upstream redirects.

- A persistent human-readable package store using original filenames, a non-public temporary area, capacity protection, cleanup, pre-population and administrative removal.

- Containerized deployment through Docker Desktop on one dedicated managed Mac.

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
| Initial deployment     | NGINX and the Jamf helper run as containers through Docker Desktop on one dedicated managed Mac.                                                                            | Resolved            |
| Client namespace       | A stable HTTPS /packages/{filename} endpoint is used.                                                                                                                      | Baseline assumption |
| Upstream               | One Jamf Pro tenant and JCDS are used in the first release.                                                                                                                | Baseline assumption |
| Storage representation | A completed request path maps to the same human-readable local path and filename; for example, /packages/ExampleFile.pkg maps to /srv/jamf-store/packages/ExampleFile.pkg. | Resolved            |
| Store rebuild          | The package store is derived data and can be rebuilt from JCDS; configuration and secrets require backup, package contents do not.                                         | Baseline assumption |
| Jamf API version       | Use the deprecated v1 JCDS resolver and file-list endpoints until Jamf introduces replacements; isolate both behind adapters.                                              | Resolved            |
| Publication integrity  | Require exact JCDS catalog `length` and SHA3-512 match before a downloaded package is atomically published.                                                                 | Resolved            |
| V1 path scope          | Accept exactly one flat filename segment ending in lowercase `.pkg`; nested paths and additional file types are excluded.                                                   | Resolved            |
| Initial population     | Design the first release for 500â€“2,000 managed Macs.                                                                                                                        | Resolved range      |
| Cache storage          | Target an approximately 500â€“600 GB package working set on the 1 TB Mac and retain at least 30 percent package-store free space.                                             | In review           |
| Host baseline          | Use a dedicated Mac running a supported macOS release and managed Docker Desktop; model, resources and unattended-startup design remain open.                              | Partially resolved  |
| Service endpoint       | Publish HTTPS on `jcds-cache.appfruit.ch:8443`.                                                                                                                             | Resolved            |
| Client access          | Use server-authenticated TLS without source-CIDR filtering or client authentication; any route-reachable client may request packages.                                      | Resolved            |
| Storage boundary       | Keep `/srv/jamf-store` as the container path and use a Docker named volume in Docker Desktop's APFS-backed disk image; Docker/administrative access from macOS satisfies the visibility requirement. | Resolved            |
| Secret delivery        | Pass the Jamf client secret through a root-owned mode-`0600` host environment file outside Git.                                                                             | Resolved            |
| DNS and certificate    | Use manual DNS records and manual DNS validation for initial certificate issuance; expiry monitoring is mandatory and unattended renewal remains a production gate.          | Pilot decision      |
| Outbound network       | Connect directly over validated HTTPS without an outbound proxy or TLS inspection.                                                                                          | Resolved            |

> **Normative language:** Must indicates a mandatory production requirement. Should indicates a recommended requirement that may only be waived through a documented design decision. May identifies an optional capability.

## 6. Stakeholders and responsibilities

| **Stakeholder**               | **Primary responsibility**                                                                                       |
|-------------------------------|------------------------------------------------------------------------------------------------------------------|
| Service owner / Mac Workplace | Own requirements, client integration, service acceptance, package naming policy and support model.               |
| Platform / infrastructure     | Provide and patch the Mac, macOS, Docker Desktop, storage, network, DNS, TLS and monitoring integration.          |
| Security                      | Review authentication, secret storage, network controls, logging, image hardening and threat model.              |
| Jamf administration           | Create the least-privilege API role/client, monitor deprecated JCDS endpoints and provide sanitized schema evidence. |
| Operations                    | Monitor the service, respond to alerts, rotate secrets, manage capacity and follow recovery procedures.          |
| Managed clients               | Request packages using the published internal URL and supported HTTP behaviour.                                  |

## 7. Solution architecture

### 7.1 Logical components

**NGINX filesystem gateway:** Terminates downstream TLS; validates method and path; maps the canonical request URI to the final filesystem path; serves existing files; routes misses internally; streams helper responses; records delivery telemetry.

**Jamf download helper:** Exposes an internal-only package endpoint; coordinates one fill per canonical package path; manages OAuth tokens; calls the JCDS catalog and resolver APIs; parses and validates metadata; validates the resulting URL; follows allowed redirects; streams and hashes the JCDS response; owns temporary files and atomic publication.

**Persistent package-store volume:** Stores final package bytes under their original URL-derived names and keeps hidden temporary downloads on the same filesystem so publication can be atomic.

**Jamf OAuth endpoint:** Issues a short-lived access token using the configured client ID and client secret.

**Jamf JCDS resolver API:** Returns a temporary download URL for the requested package filename.

**Jamf JCDS catalog API:** Returns package filenames, byte lengths, MD5 values, regions and SHA3-512 digests. Length and SHA3-512 are the publication-integrity controls.

**JCDS/CDN object endpoint:** Provides the package bytes through the temporary, time-limited URL.

**Operations integration:** Collects logs, metrics and alerts and supplies secrets, certificates and configuration.

### 7.2 Trust boundaries

- Clients can reach only the NGINX HTTPS listener. The helper must not be exposed outside the container network or host loopback interface.

- NGINX does not receive the Jamf client secret. Docker injects it only into the helper from the root-owned host environment file, and the helper retains access tokens in memory only.

- The helper may connect only to the configured Jamf tenant and approved JCDS/CDN destinations over validated TLS.

- A filename supplied by a client must never become an arbitrary upstream URL, filesystem path or shell input; traversal, encoded separators and symbolic-link escapes must be prevented.

### 7.3 Network flows

| **Source**      | **Destination**         | **Protocol**             | **Purpose**                                         |
|-----------------|-------------------------|--------------------------|-----------------------------------------------------|
| Managed client  | NGINX gateway           | TCP 8443 / HTTPS         | Retrieve packages                                   |
| NGINX gateway   | Jamf helper             | Container network / HTTP | Internal store-miss request                         |
| Jamf helper     | Jamf Pro tenant         | TCP 443 / HTTPS          | OAuth token, metadata catalog and download-URL APIs |
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

4.  The helper obtains the exact package entry from the selected Jamf JCDS catalog endpoint and validates its `length` and `sha3` fields.

5.  The helper calls the selected Jamf JCDS resolver endpoint with Authorization: Bearer {token} and Accept: application/json, extracts `uri`, and verifies its scheme, hostname, redirects and destination policy.

6.  The helper requests the complete package and rejects a declared upstream Content-Length that disagrees with the catalog before streaming begins.

7.  The helper streams response bytes to NGINX without buffering the complete object in application memory while writing and hashing the same bytes under the non-public temporary directory.

8.  After a successful complete 200 response, exact byte-count match and SHA3-512 match, the helper atomically renames the temporary file to the exact final URL-derived path. A failure, partial response, truncation or digest mismatch leaves no file in the served namespace.

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

For a store miss, the helper must use the selected deprecated Jamf APIs behind replaceable adapters until Jamf introduces replacements. It must find one exact filename entry from `GET /api/v1/jcds/files`, validate `length` and the 128-character hexadecimal `sha3` value, then call `GET /api/v1/jcds/files/{fileName}` and extract exactly one download URL from `uri`. The URL field and adapter contracts must remain configurable for future replacement APIs. An observed resolver `404` body containing `httpStatus: 404` and an empty `errors` array must map to package not found.

> **Priority: Must. Acceptance:** Valid metadata/resolver, not-found, malformed, unauthorized, duplicate-entry, incomplete-page and deprecated-endpoint responses map to documented outcomes without leaking response bodies.

### FR-008 Temporary URL validation

Before downloading, the helper must require HTTPS, validate the hostname against an approved policy, reject embedded credentials and unsafe destinations, and revalidate every redirect. Signed URL query parameters must never be logged.

> **Priority: Must. Acceptance:** SSRF and redirect tests cannot reach unapproved, private, loopback or link-local destinations.

### FR-009 Streaming store fill

On a store miss, the helper and NGINX must stream the upstream body so the first client receives bytes before the complete package is downloaded. The helper must use bounded buffers and must not hold a package in memory.

> **Priority: Must. Acceptance:** A timed test demonstrates downstream bytes before upstream completion and stable memory usage during the largest supported package.

### FR-010 Complete-file publication

Only a complete successful full-package response with status 200, an exact Jamf catalog byte-count match and a matching SHA3-512 digest may become a reusable final file. The response must first be written under a non-public temporary path on the same filesystem and then atomically renamed to the exact final path. A 206 response, error body, incomplete transfer, length mismatch or digest mismatch must never be published as the final package. MD5 may be recorded for interoperability but must not replace SHA3-512 verification.

> **Priority: Must. Acceptance:** Interrupted, truncated, partial, length-mismatched, digest-mismatched and failed transfers leave no regular file under the served final path.

Because the first client receives bytes before the complete digest is known, a final digest mismatch cannot retroactively change an already-started 200 response. The helper must terminate the fill, discard the temporary file and record a sanitized integrity failure; normal macOS package-signature validation remains the final client-side defense.

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

The package-store root, maximum usage, inactive-retention policy and minimum free-space protection must be configurable. The defaults are 90 days inactivity, cleanup below 30 percent free space and recovery to 35 percent free space. Because this is a filesystem store rather than the NGINX proxy cache, an explicit cleanup process must remove selected inactive or least-recently-requested final files without affecting configuration, secrets or active temporary downloads.

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

The first release must support 500â€“2,000 managed Macs, the measured active-download concurrency, package-size distribution and an approximately 500â€“600 GB working set while retaining at least 30 percent package-store free space. The design should permit later migration to larger storage or a redundant deployment.

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

The downstream response must be compatible with the HTTP client used by the Mac s×n¶¶‰žËkºwµçgbö¦Öb×7F÷&R÷6¶vW2ôW†×ÆTf–ÆRç¶rÀ§ÂFV×÷&'’F÷væÆöBÂ÷7'bö¦Öb×7F÷&RòçFV×÷&'’÷·Væ—VRÖ–GÒç'BÀ§Â†—BÆöö·WÂät”å‚G'•öf–ÆW26†V6·2öæÇ’F†Ræ÷&ÖÆ—¦VBf–æÂ&VwVÆ"Öf–ÆRF‚À§ÂÖ—72W'6—7FVæ6RÂF†R†VÇW"w&—FW2ÂfW&–f–W2æBFöÖ–6ÆÇ’V&Æ—6†W2g&öÒ†–FFVâFV×÷&'’7F÷&vRÀ ¥F†RF‡2&÷fRFVf–æRF†R&6VÆ–æRÆ–÷WBæBÖ’&R6†ævVBF‡&÷Vv‚6öæf–wW&F–öâÂ'WBF†RöæR×FòÖöæR&VÆF–öç6†—&WGvVVâF†R6æöæ–6Â6Æ–VçBF‚æBF†R‡VÖâ×&VF&ÆRf–æÂf–ÆW7—7FVÒF‚—2ÖæFF÷'’à ¢22"âf–ÆW7—7FVÒ6¶vR×7F÷&RFW6–vâæBFFÆ–fV7–6ÆP ¢222"ã7F÷&vRÖöFVÀ ¢ÒW6RFVF–6FVBW'6—7FVçBföÇVÖR÷"f–ÆW7—7FVÒ&ö÷FVBB6öæf–wW&&ÆRF‚7V6‚2÷7'bö¦Öb×7F÷&Rà ¢Ò7F÷&RV6‚6ö×ÆWFVB6¶vR2&r'—FW2VæFW"F†R6ÖRæ÷&ÖÆ—¦VBF‚æBf–ÆVæÖRW6VB'’F†R6Æ–VçBU$Ã²Fòæ÷Bw&f–æÂf–ÆW2–âät”å‚66†RÖWFFF÷"†6†VBf–ÆVæÖW2à ¢ÒÆ6R†–FFVâFV×÷&'’f–ÆW2öâF†R6ÖRf–ÆW7—7FVÒ2f–æÂ6¶vW2æB÷WG6–FRF†Rät”å‚×6W'fVBæÖW76R6ò7V66W76gVÂf–æÆ—¦F–öâ6âW6RâFöÖ–2&VæÖRà ¢Ò6W'fRöæÇ’fÆ–FFVB&VwVÆ"f–æÂf–ÆW2â7–Ö&öÆ–2Æ–æ·2ÂFWf–6Rf–ÆW2Â6ö6¶WG2æBf–ÆW2÷WG6–FRF†R6öæf–wW&VB&ö÷B×W7BæWfW"&RföÆÆ÷vVB÷"W‡÷6VBà ¢Ò¶VW6öæf–wW&F–öâÂDÅ2ÖFW&–ÂÂ6V7&WG2ÂÆö6·2Â÷W&F–öæÂÖWFFFæBÆöw2÷WG6–FRF†Rf–æÂ6¶vRæÖW76Rà ¢Ò&W6W'fRg&VR66—G’f÷"BÆV7BF†RÆ&vW7B7W÷'FVB–â×&öw&W72F÷væÆöBÇW2÷W&F–öæÂ†VG&ööÒà ¢ÒG&VB6¶vR×7F÷&R6öçFVçG22&V6öç7G'V7F&ÆRFW&—fVBFF²&6²W6öæf–wW&F–öâæB'Væ&öö·2Âæ÷BæV6W76&–Ç’7F÷&VB6¶vW2à ¢222"ã"6—¦–ærÖWF†ö@ £â¢¤–æ—F–Â6—¦–ærf÷&×VÆ¢¢¢W6&ÆR6¶vR×7F÷&R66—G’6†÷VÆB&RBÆV7BF†RW‡V7FVB7F—fR6¶vRv÷&¶–ær6WBÇW26öæ7W'&VçBFV×÷&'’ÖF÷væÆöBÆÆ÷væ6RÂ×VÇF—Æ–VB'’ã#f÷"÷W&F–öæÂ†VG&ööÒâF†Rf–æÂfÇVR×W7B&R6Æ7VÆFVBg&öÒ7GVÂ¤4E26¶vR–çfVçF÷'’æBF÷væÆöBFVÖæBà ¥F†Rf—'7BFWÆ÷–ÖVçBF&vWG2â&÷†–ÖFVÇ’S(	3ct"7F—fR6¶vRv÷&¶–ær6WBöâF†RD"Ö2âBÆV7B3W&6VçBöbF†R6¶vR×7F÷&Rf–ÆW7—7FVÒ×W7B&VÖ–âg&VRâ6¶vR–çfVçF÷'’ÂÖ†–×VÒö&¦V7B6—¦RæB6–×VÇFæV÷W2Öf–ÆÂFVÖæB×W7B7F–ÆÂ&RÖV7W&VB&Vf÷&RF†RFö6¶W"F—6²Ö–ÖvRÆ–Ö—BæBÆöB×FW7BÆ–Ö—G2&Rg&÷¦Vâà ¢222"ã2&WFVçF–öâæB6ÆVçW  ¤–Ö×WF&ÆRf–ÆVæÖW2W&Ö—B–æFVf–æ—FRÆö6Â&WW6Rv—F†÷WB…EEg&W6†æW72W‡—'’÷"W7G&VÒ&WfÆ–FF–öââf–æÂf–ÆW2&VÖ–âf–Æ&ÆRVçF–ÂâW‡Æ–6—BFÖ–æ—7G&F—fR&VÖ÷fÂ÷"6öæf–wW&VB66—G’Ö6ÆVçW&ö6W726VÆV7G2F†VÒâ&V6W6RF†R7F÷&vRÖöFVÂ—2æ÷Bät”å‚&÷‡•ö66†RÂ6ÆVçW×W7B&R–×ÆVÖVçFVBæBÖöæ—F÷&VB6W&FVÇ’ÂW6–ær&V6÷&FVB&WVW7B7F—f—G’÷"æ÷F†W"&÷fVB–çfVçF÷'’öÆ–7’â6ÆVçW×W7B6¶—7F—fRFV×÷&'’f–ÆW2ÂÆö6¶VBF‡2æBf–ÆW2&VÆ÷rF†R6öæf–wW&VBÖ–æ–×VÒ&WFVçF–öâW&–öBà ¢222"ãB–çFVw&—G ¢ÒFòæ÷B7F÷&R¦Öb’¥4ôâÂôWF‚W'&÷"&öF–W2Â4Dâ…DÔÂW'&÷'2Â&VF—&V7G2v—F†÷WBF†Rf–æÂ6¶vRÂ'F–Â#b&W7öç6W2÷"F—6ÆÆ÷vVB…EE7FGW6W22f–æÂ6¶vR6öçFVçBà ¢Ò&WG&–WfRF†RW†7B6FÆörVçG'’f÷"F†R&WVW7FVB–Ö×WF&ÆRf–ÆVæÖRæB&V¦V7BÖ—76–ærÂGWÆ–6FRÂÖÆf÷&ÖVB÷"–æ6ö×ÆWFR×vRÖWFFFà ¢ÒG&VB6FÆörÆVæwF†æB4„2ÓS"2F†RWF†÷&—FF—fRV&Æ–6F–öâ6†V6·2â&V¦V7B6öæfÆ–7F–ærW7G&VÒ6öçFVçBÔÆVæwF‚&Vf÷&R7G&VÖ–ærv†Vâ÷76–&ÆRÂ6÷VçBÆÂ&V6V—fVB'—FW2æB6ö×WFR4„2ÓS"–æ7&VÖVçFÆÇ’v†–ÆR7G&VÖ–ærà ¢Ò&W6W'fRF†RgVÆÂ'—FR7G&VÒv—F†÷WBG&ç6f÷&ÖF–öã²6ö×&W76–öâæB6öçFVçB&Ww&—F–ær×W7B&RF—6&ÆVBf÷"6¶vR&öF–W2à ¢ÒV&Æ—6‚öæÇ’gFW"F†RF÷væÆöFVB'—FR6÷VçBæB6ö×WFVB4„2ÓS"ÖF6‚F†R6FÆörâÔCR—2&WF–æVBöæÇ’f÷"6ö×F–&–Æ—G’÷"F–væ÷7F–72æB—2æ÷BF†R–çFVw&—G’6V7W&—G’&÷VæF'’à ¢Ò6Æ–VçB×6–FR6¶vR6–væGW&RfW&–f–6F–öâ&VÖ–ç2'BöbF†Ræ÷&ÖÂÖ4õ2–ç7FÆÆF–öâG'W7B6†–âæB—2æ÷B&WÆ6VB'’F†RÆö6Â6¶vR7F÷&Rà ¢222"ãR&R×÷VÆF–öâæBÖçVÂ6†ævW0 ¢Òâ÷W&F÷"Ö’&R×÷VÆFRâ&÷fVB6¶vR'’w&—F–ær—BFòF†Ræöâ×V&Æ–27Fv–ær&VÂÇ––ærF†R&WV—&VB÷væW'6†—æBW&Ö—76–öç2ÂfÆ–FF–ær—BÂæBFöÖ–6ÆÇ’&VæÖ–ær—BFòF†R6æöæ–6Âf–æÂF‚à ¢ÒF—&V7Bw&—FW2–çFòV&Æ–6Ç’6W'fVBf–æÂf–ÆVæÖR&R&ö†–&—FVB&V6W6R6Æ–VçB6÷VÆBö'6W'fR'F–Âf–ÆRà ¢ÒW†—7F–ærf–æÂf–ÆW2×W7Bæ÷B&R÷fW'w&—GFVââ6÷'&V7FVB6¶vR&V6V—fW2æWr–Ö×WF&ÆRf–ÆVæÖS²&VÖ÷fÂöbâöÆBf–ÆR—26W&FRVF—FVB7F–öâà ¢ÒF†R6W'f–6R×W7BFWFV7BæB&W÷'BVæW‡V7FVBf–ÆW2ÂVç6fRW&Ö—76–öç2÷"7–Ö&öÆ–2Æ–æ·2GW&–ær÷W&F–öæÂfÆ–FF–öâà ¢222âFWÆ÷–ÖVçBæB÷W&F–öç0 ¢2222ã6öçF–æW"FWÆ÷–ÖVç@ §Â¢¤6ö×öæVçB¢¢Â¢¥W'÷6R¢¢Â¢¤W‡÷7W&R¢¢À§ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§Âæv–ç‚ÂDÅ2Æ—7FVæW"öâƒCC2ÂG'•öf–ÆW2Æöö·WÂ7FF–2FVÆ—fW'’Â–çFW&æÂÖ—72&÷WF–æræBF÷vç7G&VÒ7G&VÖ–ærÂ6Æ–VçBæWGv÷&²²–çFW&æÂ†VÇW"À§Â¦ÖbÖF÷væÆöBÖ†VÇW"Â6–ævÆRÖfÆ–v‡B6ö÷&F–æF–öâÂôWF‚Â6FÆör÷&W6öÇfW"FFW'2ÂU$ÂöÆ–7’Â†6†–ærÂFV×÷&'’f–ÆW2æBFöÖ–2V&Æ–6F–öâÂ–çFW&æÂ6öçF–æW"æWGv÷&²öæÇ’À§ÂW'6—7FVçB6¶vR×7F÷&RföÇVÖRÂ‡VÖâ×&VF&ÆRf–æÂ6¶vW2æB†–FFVâFV×÷&'’f–ÆW2Âw&—F&ÆRöæÇ’'’F†R6¶vR×V&Æ–6F–öâF‚À§Â6V7&WG2ö6W'F–f–6FW2Â¦Öb6Æ–VçB6V7&WB–â&ö÷BÖ÷væVB†÷7BVçf—&öæÖVçBf–ÆS²DÅ2¶W’æBG'W7B6öæf–wW&F–öâ–âF†R†÷7B6W'F–f–6FRG&VRÂ†VÇW"Vçf—&öæÖVçBöæÇ’òät”å‚&VBÖöæÇ’Ö÷VçBÀ ¢ÒFö6¶W"FW6·F÷×W7B&RÆ–6Vç6VBf÷"F†RVçFW'&—6RÂ7W÷'FVBöâF†R6VÆV7FVBÖ4õ2&VÆV6RÂÖævVBF‡&÷Vv‚F†R&÷fVBÖ2ÖÖævVÖVçB6öçG&öÇ2æB76–væVBW‡Æ–6—B5RÂÖVÖ÷'’æBF—6²Æ–Ö—G2à ¢ÒFö6¶W"FW6·F÷&W6÷W&6R6fW"×W7B&RF—6&ÆVBf÷"F†—2Çv—2Ööâ6W'f–6RâÖ4õ2æBFö6¶W"FW6·F÷WFFW2×W7BW6R6öçG&öÆÆVBÖ–çFVææ6Rv–æF÷w2v—F‚÷7B×WFFR†VÇF‚fÆ–FF–öâà ¢Ò–ÖvW2×W7BW6R&÷fVB&Vv—7G&–W2Â–ææVBfW'6–öç2÷"F–vW7G2æBFö7VÖVçFVBF6‚6FVæ6Rà ¢ÒF†R†VÇW"6†÷VÆB&R6ÖÆÂ7FF–6ÆÇ’6ö×–ÆVB6W'f–6RÂ7V6‚2vòÂv—F‚&÷VæFVB7G&VÖ–ær'VffW'2æBæò6†VÆÂW†V7WF–öâà ¢ÒF†R&öGV7F–öâ†VÇW"6†÷VÆB'Vâ2æöâ×&ö÷BT”Bâ–bFö6¶W"FW6·F÷w2æÖVB×föÇVÖR÷væW'6†—ÖöFVÂ&WfVçG2F†—2ÂT”BÖ’&R6öç6–FW&VBöæÇ’F‡&÷Vv‚â&÷fVBW†6WF–öâv—F‚ÆÂ6&–Æ—F–W2G&÷VBÂæòÖæWr×&—f–ÆVvW6Â&VBÖöæÇ’&ö÷Bf–ÆW7—7FVÒæBF†R6¶vRföÇVÖR2—G2öæÇ’w&—F&ÆRFFF‚âät”å‚Ö’&WF–â—G27FæF&B&ö÷BÖ7FW"6öÆVÇ’Fò&VBF†R6W'F–f–6FRæB7v—F6‚FòVç&—f–ÆVvVBv÷&¶W'3²—B×W7BG&÷ÆÂVç&VÆFVB6&–Æ—F–W2à ¢ÒöæÇ’F†R6¶vR×7F÷&R7Fv–æröf–æÆ—¦F–öâF‚æBW‡Æ–6—FÇ’&WV—&VB'VçF–ÖRF—&V7F÷&–W2Ö’&Rw&—F&ÆS²7FF–2×6W'f–ærv÷&¶W'26†÷VÆB†fRF†RÖ–æ–×VÒ&WV—&VBw&—FR66W72à ¢Ò7F'GW×W7Bf–Â6ÆV&Ç’öâ–çfÆ–B6öæf–wW&F–öâÂÖ—76–ær6V7&WG2÷"–çfÆ–BDÅ2ÖFW&–Âà ¢Ò&öGV7F–öâ&VF–æW72×W7BFVÖöç7G&FRVæGFVæFVB&V6÷fW'’gFW"Ö2&V&ö÷BÂFö6¶W"FW6·F÷&W7F'BæBÖævVBWFFRâF†RFW6–vâ×W7B7FFRv†WF†W"FVF–6FVBÖ4õ266÷VçB×W7B&VÖ–âÆövvVB–âà ¢ÒF†RÆö6Æ†÷7BÖöæÇ’FWÆ÷’öÖ6÷2ö&öf–ÆR×W7Bæ÷B&RW‡÷6VBFòF†RÄââ&öGV7F–öâ&WV—&W26W&FRDÅ2ÖVæ&ÆVBÖ4õ26ö×÷6R&öf–ÆRà ¢2222ã"6öæf–wW&F–öà §Â¢¤&V¢¢Â¢¥&WV—&VBW‡FW&æÂ6öæf–wW&F–öâ¢¢À§ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§Â6W'f–6RÂ†÷7FæÖRÂÆ—7FVâ÷'BÂDÅ26W'F–f–6FRö¶W’F‡2Â6Æ–VçB66W72öÆ–7’À§Â¦ÖbÂFVæçB&6RU$ÂÂôWF‚F‚Â6FÆöræB&W6öÇfW"FFW"fW'6–öç2Â’6Æ–VçB”BæB6V7&WB&VfW&Væ6RÀ§ÂW7G&VÒöÆ–7’ÂÆÆ÷vVB6–væVBÕU$Â†÷7BGFW&ç2Â&VF—&V7BÆ–Ö—BÂDå2ô•&W7G&–7F–öç2ÂDÅ2G'W7BÀ§Â6¶vR7F÷&RÂ6öçF–æW"&ö÷B÷FV×÷&'’F‡2ÂFö6¶W"föÇVÖR÷"e2&6¶–ærF‚ÂFö6¶W"F—6²Ö–ÖvRÆö6F–öâöÆ–Ö—BÂ6ÆVçWÂÆö6²F–ÖV÷WBÂÖ–æ–×VÒg&VR76RÂ÷væW'6†—æBW&Ö—76–öç2À§Â…EEÂ6öææV7B÷&VB÷6VæBF–ÖV÷WG2ÂÖ†–×VÒ6¶vR6—¦RÂ&ævRöÆ–7’Â6Æ–VçBÖ&÷'B&V†f–÷W"À§Âö'6W'f&–Æ—G’ÂÆörf÷&ÖBöÆWfVÂÂÖWG&–72Æ—7FVæW"Â6÷'&VÆF–öâ†VFW"æBÖöæ—F÷&–ærFW7F–æF–öâÀ ¢2222ã2Ööæ—F÷&–æræBÆW'F–æp ¥F†R&6VÆ–æRät”å‚&V†f–÷"ÖÆör66†VÖæB&—f7’&÷VæF'’&RFVf–æVB–âFö72ö6Æ–VçB×&WVW7BÖÖöæ—F÷&–æræÖFâ&öGV7F–öâ6öÆÆV7F–öâÖ’Vç&–6‚&V6÷&G2v—F‚FWÆ÷–ÖVçBÖWFFFÂ'WB—B×W7Bæ÷B&V–çG&öGV6RF†RW†6ÇVFVBU$’Â6¶vR–FVçF—G’Â&r†VFW'2Â7&VFVçF–Ç2÷"6–væVBU$Ç2à ¢ÒÆW'Bv†VâF†R6W'f–6R÷"†VÇW"—2æ÷B&VG’f÷"ÆöævW"F†âF†Rw&VVBw&6RW&–öBà ¢ÒÆW'BöâÆ÷r6¶vR×7F÷&Rf–ÆW7—7FVÒg&VR76R&Vf÷&R7F—fRF÷væÆöG2&RB&—6²à ¢ÒÆW'Böâ7W7F–æVB¦ÖbWF†VçF–6F–öâf–ÇW&W2Â&W6öÇfW"f–ÇW&W2÷"6–væVBÕU$ÂöÆ–7’&V¦V7F–öç2à ¢ÒÆW'Böâ&æ÷&ÖÂW‡‚&FRÂ–æ6ö×ÆWFRG&ç6fW'2ÂVç6fRf–ÆW7—7FVÒö&¦V7G2Â6öçF–æW"&W7F'BÆö÷2÷"6¶vR×V&Æ–6F–öâf–ÇW&W2à ¢ÒF6†&ö&BÆö6Â†—B&F–òæB'—FW2fö–FVBÂ'WB–çFW'&WBÆ÷r†—B&F–òv–ç7B6¶vR&VÆV6RGFW&ç2&F†W"F†â2â—6öÆFVBfVÇBà ¢Ò&WF–â6æ—F—¦VB66W72æB6W'f–6RÆöw266÷&F–ærFòVçFW'&—6R÷W&F–öæÂÖÆöröÆ–7’à ¢2222ãB&V6÷fW'’æB6öçF–çV—G ¢ÒgFW"6öçF–æW"&W7F'B÷"†÷7B&V&ö÷BÂ6ö×ÆWFRf–æÂf–ÆW2×W7B&VÖ–âW6&ÆRæB7FÆRFV×÷&'’f–ÆW2×W7B&R†æFÆVB66÷&F–ærFòöÆ–7’à ¢ÒFö6¶W"FW6·F÷æBF†R6ö×÷6RÆ–6F–öâ×W7B&V6÷fW"v—F†÷WBÖçVÂ–çFW'fVçF–öâgFW"F†R&÷fVBÖ4õ27F'GW6WVVæ6Râ6öçF–æW"&W7F'BöÆ–6–W2&Ræ÷B7Vff–6–VçBVæÆW72Fö6¶W"FW6·F÷—G6VÆb7F'G27V66W76gVÆÇ’à ¢Ò–bF†R6¶vR×7F÷&RföÇVÖR÷"Fö6¶W"FW6·F÷dÒF—6²—2Æ÷7BÂF†R6W'f–6RÖ’7F'Bv—F‚âV×G’7F÷&RæB&W÷VÆFRöâFVÖæBgFW"7F÷&vR—2&W7F÷&VBâ6öæf–wW&F–öâÂ6W'F–f–6FW2æBFWÆ÷–ÖVçBÖWFFF×W7B&R&V6÷fW&&ÆR–æFWVæFVçFÇ’öb66†VB6¶vR'—FW2à ¢Ò–b¦Öbô¤4E2—2Væf–Æ&ÆRÂÆö6ÆÇ’7F÷&VB–Ö×WF&ÆR6¶vW2&VÖ–âf–Æ&ÆS²'6VçB6¶vW2f–Â&VF–7F&Ç’à ¢Ò&öÆÆ&6²×W7B&W7F÷&RF†R&–÷"¶æ÷vâÖvööB–ÖvW2æB6öæf–wW&F–öâv—F†÷WBFVÆWF–ærF†R6¶vR7F÷&RVæÆW72–æ6ö×F–&–Æ—G’&WV—&W2—Bà ¢Ò7&VFVçF–Â6ö×&öÖ—6R&WV—&W2’6Æ–VçB×6V7&WB&÷FF–öâÂ&ö6W72&W7F'B÷&VÆöBÂÆör&Wf–WræB&Wfö6F–öâ66÷&F–ærFòF†R6V7W&—G’'Væ&öö²à ¢22BâfW&–f–6F–öâæB66WFæ6P §Â¢¤”B¢¢Â¢¥FW7B¢¢Â¢¥72Wf–FVæ6R¢¢À§ÂÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§ÂBÓÂ7F÷&RÖ—72Â&WG&–WfRâ'6VçB6¶vS²fW&–g’öæR¦Öb&W6öÇWF–öâÂöæR¤4E2G&ç6fW"ÂÔ•52FVÆVÖWG'’æBV&Æ–6F–öâVæFW"F†RW†7B6æöæ–6Âf–æÂf–ÆVæÖRâÀ§ÂBÓ"Â7G&VÖ–ærÂF‡&÷GFÆRF†RW7G&VÓ²fW&–g’F†R6Æ–VçB&V6V—fW2'—FW2&Vf÷&RW7G&VÒ6ö×ÆWF–öâæBÖVÖ÷'’&VÖ–ç2&÷VæFVBâÀ§ÂBÓ2ÂÆö6Â†—BÂ&WVBF†R&WVW7C²fW&–g’–FVçF–6Â'—FW2&R6W'fVBg&öÒF†R‡VÖâ×&VF&ÆRf–æÂF‚v—F‚Äô4Âô„•BFVÆVÖWG'’æBæò†VÇW"ô¦Öb6ÆÂâÀ§ÂBÓBÂ6öæ7W'&VçBÖ—72Â&WVW7BöæRÆö6ÆÇ’'6VçB6¶vRg&öÒ×VÇF—ÆR6Æ–VçG3²fW&–g’öæRW7G&VÒf–ÆÂæB6÷'&V7Bv—F–ærÖ6Æ–VçB&W7VÇG2âÀ§ÂBÓRÂ6Æ–VçB&÷'BÂF—66öææV7BF†R–æ—F–F–ær6Æ–VçC²fW&–g’F†R7F÷&Rf–ÆÂ6ö×ÆWFW2ÂF†Rf–æÂf–ÆR—2FöÖ–6ÆÇ’V&Æ—6†VBæBF†RæW‡B&WVW7B—2fÆ–BÆö6Â„•BâÀ§ÂBÓbÂFö¶Vâ&WW6RÂ&WVW7B6WfW&ÂÆö6ÆÇ’'6VçB6¶vW2v—F†–âöæRFö¶VâÆ–fWF–ÖS²fW&–g’6†&VBFö¶VââÀ§ÂBÓrÂFö¶VâW‡—'’óCÂf÷&6RW‡—&F–öâæBC²fW&–g’V&Ç’&VæWvÂæBBÖ÷7BöæRWF†VçF–6FVB&WG'’âÀ§ÂBÓ‚Âæ÷Bf÷VæBÂ&WVW7Bf–ÆVæÖR'6VçBg&öÒ¦Öc²fW&–g’6æ—F—¦VBCBæBæòf–æÂW'&÷"Ö&öG’f–ÆRâÀ§ÂBÓ’Â–çWBGF6·2ÂFW7BG&fW'6ÂÂVæ6öF–ærÂ'6öÇWFRU$Ç2Â6öçG&öÂ6†&7FW'2æB÷fW'6—¦VBæÖW3²fW&–g’&V¦V7F–öâv—F†÷WBW7G&VÒ6ÆÇ2âÀ§ÂBÓÂ55$b÷&VF—&V7BÂ&WGW&âVç6fRU$Ç2æB&VF—&V7B6†–ç2g&öÒÖö6²&W6öÇfW#²fW&–g’WfW'’Vç6fRFW7F–æF–öâ—2&Æö6¶VBâÀ§ÂBÓÂG&ç6fW"–çFVw&—G’ÂFW&Ö–æFR7G&VÒV&Ç’æB–æ¦V7Bw&öærÆVæwF‚æB4„2ÓS"ÖWFFF²fW&–g’FV×÷&'’7FFR—2F—66&FVBæBæòf–æÂf–ÆRW†—7G2âÀ§ÂBÓ"Â&ævRæB„TBÂ'VâF†R7GVÂÖ26Æ–VçB&WVW7BGFW&ç2öâÆö6Â†—G2æB7F÷&RÖ—76W3²fW&–g’7FF–2&ævR&W7öç6W2æBF†BÖ—72æWfW"V&Æ—6†W2'F–Â#b&öG’2F†Rf–æÂf–ÆRâÀ§ÂBÓ2ÂF—6²&W77W&RÂ&V6‚6öæf–wW&VB66—G’æBÖ–æ–×VÒÖg&VR×76RF‡&W6†öÆG3²fW&–g’6ÆVçWöÆW'G2æB6öçG&öÆÆVBf–ÇW&Rv—F†÷WBFVÆWF–ær7F—fRf–ÆÇ2âÀ§ÂBÓBÂ&W7F'B&V6÷fW'’Â&W7F'B6öçF–æW'2æB&V&ö÷BF†R†÷7C²fW&–g’6ö×ÆWFRVçG&–W27W'f—fRæBFV×÷&'’7FFR—2†æFÆVB6fVÇ’âÀ§ÂBÓRÂW7G&VÒ÷WFvRÂF—6&ÆR¦Öbô¤4E3²fW&–g’Æö6ÆÇ’7F÷&VB6¶vW2&VÖ–âf–Æ&ÆRæB'6VçB6¶vW2f–Â&VF–7F&Ç’âÀ§ÂBÓbÂDÅ2ÂfÆ–FFRG'W7FVB66W72æB&V¦V7F–öâöbW‡—&VBÂVçG'W7FVBæB†÷7FæÖRÖÖ—6ÖF6†VB6W'F–f–6FW2âÀ§ÂBÓrÂ6V7&WB&VF7F–öâÂ–ç7V7B&W7öç6W2ÂÆöw2ÂÖWG&–72Â7&6‚÷WGWBÂFV×÷&'’f–ÆW2æBF†Rf–æÂ6¶vRæÖW76Rf÷"7&VFVçF–Ç2ÂFö¶Vç2æB6–væVBU$Ç2âÀ§ÂBÓ‚Â7F÷&RFÖ–æ—7G&F–öâÂFöÖ–6ÆÇ’&R×÷VÆFRöæRfÆ–FFVB6¶vRæBfW&–g’Æö6Â„•Bv—F†÷WB¦Öb66W73²&VÖ÷fR—BF‡&÷Vv‚F†RFÖ–æ—7G&F—fR&ö6VGW&RÂfW&–g’F†RVF—B&V6÷&BæBö'6W'fR7V'6WVVçBÔ•52âÀ§ÂBÓ’ÂÆöBÂFW7Bw&VVB6öæ7W'&VçB6Æ–VçG2ÂÆ&vW7B6¶vRæB†—BF‡&÷Vv‡WBv—F†÷WB&W6÷W&6RW††W7F–öââÀ§ÂBÓ#ÂFFW"6ö×F–&–Æ—G’Â'Vâ6FÆöræB&W6öÇfW"6öçG&7BFW7G2v–ç7BF†R6VÆV7FVBFW&V6FVB&W7öç6W2æBÖö6¶VBgWGW&R&WÆ6VÖVçBFFW'2âÀ ¢222Bã&öGV7F–öâ66WFæ6RvFW0 ¢ÒÆÂ×W7B&WV—&VÖVçG2&R–×ÆVÖVçFVB÷"†fRâ&÷fVBW†6WF–öâv—F‚&—6²÷væW"æBW‡—'’FFRà ¢ÒF†RW†7B¦ÖbVæGö–çBÂ&W7öç6R66†VÖÂW&Ö—76–öç2æB6–væVBÕU$ÂFW7F–æF–öâöÆ–7’&RfÆ–FFVB–âF†R&öGV7F–öâFVæçBà ¢Ò6V7W&—G’&Wf–WræBF‡&VBÖÖöFVÂ7F–öç2&R6ö×ÆWFRà ¢Ò66—G’æBW&f÷&Öæ6RF&vWG2&R&÷fVBæB76VBà ¢ÒÖöæ—F÷&–ærÂÆW'F–ærÂ÷væW'6†—ÂöâÖ6ÆÂöW66ÆF–öâæB'Væ&öö·2&R÷W&F–öæÂà ¢Ò6Æ–VçB6ö×F–&–Æ—G’—2FVÖöç7G&FVBöâ&W&W6VçFF—fRÖævVBÖ2æB–ç7FÆÆF–öâv÷&¶fÆ÷rà ¢222Bã"7W'&VçBÓWFöÖFVBWf–FVæ6P ¥F†RÖö6²ÖG&—fVâÖ–ÆW7FöæR7W'&VçFÇ’&÷f–FW2WFöÖFVBWf–FVæ6Rf÷"BÓF‡&÷Vv‚BÓRÂF†RVç6fR×&VF—&V7B÷'F–öâöbBÓÂF†RG'Væ6F–öâöÆVæwF‚öF–vW7B÷'F–öâöbBÓÂF†R&÷f—6–öæÂÖ—72öÆö6Â×&ævR÷'F–öâöbBÓ"Â6W'f–ærÖ6öçF–æW"W'6—7FVæ6Rg&öÒBÓBÂæBFWVæFVæ7’Ö–æFWVæFVçBÆö6ÂFVÆ—fW'’g&öÒBÓRâF†RFö6¶W"6ö×÷6R6Öö¶RFW7B6÷fW'2F†RFWÆ÷–VBät”å‚ö†VÇW"F‚æBfW&–f–W2F†BW7G&VÒ&WVW7B6÷VçFW'2&VÖ–âVæ6†ævVBf÷"&WVFVBÆö6Â†—BÂÆö6Â&ævR&WVW7BæB&WVW7BgFW"6W'f–ærÖ6öçF–æW"&W7F'Bâ—BF†Vâ7F÷2F†RÖö6²W7G&VÒÂ6öæf—&×2F†R6ö×ÆWFVB6¶vR&VÖ–ç2Æö6ÆÇ’f–Æ&ÆRæBfW&–f–W2F†Bâ'6VçB6¶vR&V6V—fW26öçG&öÆÆVBW'&÷"v—F†÷WB7&VF–ærf–æÂf–ÆRà ¥F†R†VÇW"Ç6ò†2WFöÖFVB7FGW2æB&VF7F–öâ6÷fW&vRf÷"ôWF‚&V¦V7F–öâ÷F‡&÷GFÆ–ær÷F–ÖV÷WG2Â¦ÖbC6–ævÆR×&WG'’&V†f–÷"ÂC6ÂC#–ÂW‡†ÂÖÆf÷&ÖVB&W7öç6W2ÂVç6fR&VF—&V7G2æBö&¦V7Bf–ÇW&W2â6Æ–VçB&W7öç6W2æBF–væ÷7F–26FVv÷&–W2&RFW7FVB–æFWVæFVçFÇ’g&öÒFWVæFVæ7’&W7öç6R&öF–W2ÂæBG&ç7÷'Bf–ÇW&W2&R6æ—F—¦VB&Vf÷&R6ö×ÆWFR&WVW7BU$Â6÷VÆB&V6‚æ÷&ÖÂÆöw2à ¥F†—2Wf–FVæ6RFöW2æ÷B6Æ÷6RF†R&öGV7F–öâvFW2â7GVÂÖævVBÔÖ2G&ff–2—27F–ÆÂ&WV—&VBf÷"BÓ"Â7GVÂ¤4E2FW7F–æF–öâæB&VF—&V7Bö'6W'fF–öç2&R&WV—&VBf÷"BÓÂæB†÷7B&V&ö÷BÇW2–â×&öw&W72&W7F'B66W2&VÖ–âf÷"BÓBà ¢Ò&öÆÆ&6²Â6V7&WB&÷FF–öâÂ6W'F–f–6FR&VæWvÂæB6¶vR×7F÷&RÖÆ÷72&V6÷fW'’&RW†W&6—6VBà ¢22Râ&—6·2æBÖ—F–vF–öç0 §Â¢¥&—6²¢¢Â¢¤–×7B¢¢Â¢¥&–Ö'’Ö—F–vF–öâ¢¢À§ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§ÂFW&V6FVB¦ÖbVæGö–çG2ÂF†R6VÆV7FVB&W6öÇfW"æB6FÆörVæGö–çG2Ö’&R&VÖ÷fVB÷"6†ævVB&Vf÷&R¦Öb–çG&öGV6W2&WÆ6VÖVçG2âÂ66WBæBÖöæ—F÷"F†RFWVæFVæ7’&—6³²—6öÆFR&÷F‚&V†–æBfW'6–öæVBFFW'2æBÖ–w&FRv—F†÷WB6†æv–ær6Æ–VçBU$Ç2v†Vâ&WÆ6VÖVçG2V"âÀ§ÂÖ2ôFö6¶W"FW6·F÷÷WFvRÂÖ2ÂW6W"6W76–öâÂFö6¶W"FW6·F÷dÒ÷"7F÷&vRf–ÇW&RÖ¶W2F†R6W'f–6RVæf–Æ&ÆRâÂ&÷fRVæGFVæFVB&V6÷fW'’ÂÖöæ—F÷"WfW'’Æ–W"ÂFö7VÖVçBF†R6–ævÆRÖæöFR&—6²æBFVf–æRÆFW"„÷F–öâ–bF†R4Äò&WV—&W2—BâÀ§ÂFö6¶W"FW6·F÷Æ–fV7–6ÆRÂ&W6÷W&6R6fW"ÂÆ–6F–öâWFFW2ÂÖ4õ2WFFW2÷"Ö—76–ærW6W"6W76–öâ6â7F÷F†R6W'f–6RâÂF—6&ÆR&W6÷W&6R6fW"ÂÖævR6WGF–æw2æBWFFW2ÂW6RFVF–6FVB÷W&F–ærÖöFVÂæBFW7B&V&ö÷B÷WFFR&V6÷fW'’&Vf÷&R–Æ÷BâÀ§ÂFö6¶W"dÒF—6²W††W7F–öâÂæÖVB×föÇVÖRw&÷wF‚6âW††W7BFö6¶W"w2dÒF—6²–ÖvR÷"F†RVæFW&Ç––ære2föÇVÖRâÂ6WBW‡Æ–6—BF—6²Æ–Ö—G2ÂÖöæ—F÷"Fö6¶W"æBÖ4õ2g&VR76RÂ&W6W'fR3W&6VçB6¶vR×7F÷&Rg&VR76RæBW†W&6—6R6ÆVçW÷&V6÷fW'’âÀ§Â6–væVBÕU$ÂFW7F–æF–öâG&–gBÂ¤4E2ô4Dâ†÷7FæÖW2Ö’6†ævRæB'&V²7G&–7BÆÆ÷vÆ—7BâÂ&6RF†RöÆ–7’öâ¦Öb×V&Æ—6†VBFöÖ–ç2ÂÖöæ—F÷"&V¦V7F–öç2æBW6R6öçG&öÆÆVB6öæf–wW&F–öâ6†ævR&F†W"F†â&&—G&'’Vw&W72âÀ§ÂVç6fR÷"'F–ÂÖf–ÆRV&Æ–6F–öâÂâ–çFW''WFVBG&ç6fW"÷"F×W&VBf–ÆW7—7FVÒö&¦V7B6÷VÆB&RW‡÷6VB2fÆ–B6¶vRâÂ6W'fR&VwVÆ"f–ÆW2öæÇ“²FVç’7–Ö&öÆ–2Æ–æ·3²¶VWFV×÷&'’f–ÆW2÷WG6–FRF†R6W'fVBæÖW76S²V&Æ—6‚öæÇ’6ö×ÆWFRfÆ–FFVB#&W7öç6R'’FöÖ–2&VæÖS²VF—BW&Ö—76–öç2æBVæW‡V7FVBö&¦V7G2âÀ§Â&ævR&WVW7B'—72Â6Æ–VçG2F†B&WVW7BöæÇ’&ævW2Ö’&WfVçBgVÆÂÖö&¦V7B7F÷&R÷VÆF–öâ÷"6W6RGWÆ–6FRG&ff–2âÂ6GW&R7GVÂ6Æ–VçB&V†f–÷W#²f÷&6R6ö×ÆWFRW7G&VÒ&WG&–WfÂöâÖ—72æB6W'fR&ævW2g&öÒF†R6ö×ÆWFVBÆö6Âf–ÆRâÀ§Â6V7&WBF—66Æ÷7W&RÂFö¶Vç2÷"6–væVBU$Ç26÷VÆBV"–âÆöw2÷"F–væ÷7F–72âÂW6R7G'V7GW&VB&VF7F–öâÂfö–B&rW7G&VÒU$Ç2æBWF†÷&—¦F–öâ†VFW'2ÂæB–æ6ÇVFRÆör–ç7V7F–öâ–â66WFæ6RâÀ§ÂF—6²W††W7F–öâÂÆ&vR6¶vW2÷"6öæ7W'&VçBf–ÆÇ26÷VÆB6öç7VÖR†÷7B7F÷&vRâÂFVF–6FVBföÇVÖRÂÖ†–×VÒ×W6vRöÆ–7’ÂÖ–æ–×VÒÖg&VR×76RF‡&W6†öÆBÂ†VG&ööÒÂW‡Æ–6—B6ÆVçWæBÆW'G2âÀ§Âf–ÆVæÖR&WW6RÂ&WÆ6–ær'—FW2VæFW"âW†—7F–æræÖRv÷VÆBÖ¶RÆöærÖÆ—fVBÆö6Âf–ÆW2–æ6÷'&V7BâÂVæf÷&6R–Ö×WF&ÆR÷fW'6–öæVBæÖ–æræB&WV—&RW‡Æ–6—B&VÖ÷fÂÇW2æWræÖRf÷"6÷'&V7FVB6öçFVçBâÀ ¢22bâ÷VâVW7F–öç2æBwV–FVBFV6—6–öç0 ¥F†RföÆÆ÷v–ær—FV×2&R–çFVçF–öæÆÇ’W‡Æ–6—Bâ&V6öÖÖVæFVBFVfVÇG2W&Ö—BFWF–ÆVBFW6–vâFò&ö6VVBÂ'WBF†R7FFVBFV6—6–öâFVFÆ–æR6†÷w2v†Vâ6öæf—&ÖF–öâ&V6öÖW2ÖæFF÷'’à ¢2222õÓ(	B¦Öb’6öçG&7B…$U4ôÅdTB ¢Ò¢¤ö'6W'fVBWf–FVæ6S¢¢¢F†RFW&V6FVBtUBö’÷cö¦6G2öf–ÆW2÷¶f–ÆTæÖWÖVæGö–çB&WGW&ç27V66W76gVÂ¥4ôâö&¦V7Bv†÷6R6–væVBF÷væÆöBU$Â—2–âW&–²â'6VçBf–ÆR&WGW&ç2…EECBv—F‚‡GG7FGW6æBâV×G’W'&÷'6'&’à¢Ò¢¤FV6—6–öã¢¢¢W6RF†—2FW&V6FVBVæGö–çBVçF–Â¦Öb–çG&öGV6W2&WÆ6VÖVçBâ¶VW—B&V†–æB6öæf–wW&&ÆRFFW"ÂÖöæ—F÷"¦ÖbFW&V6F–öâæ÷F–6W2æB6GW&R&VÖ–æ–æræöâÓCBW'&÷"&W7öç6W2f÷"&W6–Æ–Væ6RFW7G2à¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢Ö–w&F–öâG&–vvW"æB'Væ&öö²&Vf÷&R&öGV7F–öâ&÷fÀ ¢2222õÓ"(	B6Æ–VçB66W726öçG&öÂ…$U4ôÅdTB ¢Ò¢¤FV6—6–öã¢¢¢W6R6W'fW"ÖWF†VçF–6FVBDÅ2v—F†÷WB6÷W&6RÔ4”E"f–ÇFW&–ær÷"6Æ–VçBWF†VçF–6F–öââç’æWGv÷&²6Æ–VçB&ÆRFò&÷WFRFòD5ƒCC2Ö’&WVW7B¶æ÷vâ6¶vRf–ÆVæÖRæBG&–vvW"âW7G&VÒf–ÆÂà¢Ò¢¤66WFVB6öç6WVVæ6S¢¢¢G&ç7÷'B—2WF†VçF–6FVBæBVæ7'—FVBÂ'WB6Æ–VçG2&Ræ÷BWF†÷&—¦VBâæWGv÷&²W‡÷7W&RæB6¶vRÖæÖR6öæf–FVçF–Æ—G’&R÷WG6–FRF†RÆ–6F–öâ&÷VæF'’à ¢2222õÓ2(	Bv÷&¶ÆöBæB66—G’„”â$Ud”Ur ¢Ò¢¤6öæf—&ÖVB&ævS¢¢¢F†Rf—'7B&VÆV6R×W7B7W÷'BS(	3"ÃÖævVBÖ72à¢Ò¢¤FV6—6–öâ7F–ÆÂ&WV—&VC¢¢¢v†B6¶vR6÷VçBÂF÷FÂv÷&¶–ær×6WB'—FW2ÂÖ†–×VÒ6¶vR6—¦RæBV²6–×VÇFæV÷W2Öf–ÆÂ6÷VçB×W7B&R7W÷'FVCð¢Ò¢¥&V6öÖÖVæFVBæW‡B7FW¢¢¢ÖV7W&R&V6VçB–çfVçF÷'’æBFVÖæBÂF†VâÆöB×FW7BF†R7F—fRv÷&¶–ær6WBæB6öæ7W'&VçBFV×÷&'’ÆÆ÷væ6Rv—F‚F†R3W&6VçBg&VR×76RfÆö÷"à¢Ò¢¥&WV—&VB'“¢¢¢FWF–ÆVBFW6–vâæB&ö7W&VÖVç@ ¢2222õÓB(	Bf–Æ&–Æ—G’ö&¦V7F—fR„õTâ ¢Ò¢¤FV6—6–öâ&WV—&VC¢¢¢v†Bf–Æ&–Æ—G’õ4ÄòæB&V6÷fW'’F–ÖR&R&WV—&VBf÷"&öGV7F–öâ6W'f–6Sð¢Ò¢¥&V6öÖÖVæFVBFVfVÇC¢¢¢f÷"F†R6VÆV7FVB6–ævÆRÖ†÷7Bf—'7BFWÆ÷–ÖVçBÂ&÷÷6R“’ãRW&6VçBW†6ÇVF–ær&÷fVBÖ–çFVææ6RæBFö7VÖVçBF†R6–ævÆRÖ†÷7BÆ–Ö—FF–öã²Ö÷fRFò&VGVæFçB†÷7G2–b†–v†W"4Äò—2&WV—&VBà¢Ò¢¥&WV—&VB'“¢¢¢&öGV7F–öâ&÷fÀ ¢2222õÓR(	B&ævR&WVW7G2„õTâ ¢Ò¢¤FV6—6–öâ&WV—&VC¢¢¢FöW2F†R7GVÂÖ2–ç7FÆÆF–öâ6Æ–VçB—77VR„TBÂ&ævRg&öÒ'—FR¦W&òÂ&W7VÖVBæöç¦W&ò&ævR÷"×VÇF’×&ævR&WVW7G3ð¢Ò¢¥&V6öÖÖVæFVBFVfVÇC¢¢¢6GW&R&WVW7G2g&öÒF†R&VÂ6Æ–VçBâöâÖ—72Â&WG&–WfRæBV&Æ—6‚F†R6ö×ÆWFRö&¦V7C²FWFW&Ö–æRv†WF†W"F†R&WVW7F–ær6Æ–VçBÖ’&V6V—fRgVÆÂ#&W7öç6R÷"×W7Bv—Bf÷"6÷'&V7B#b&W7öç6Rg&öÒF†R6ö×ÆWFVBf–ÆRà¢Ò¢¥&WV—&VB'“¢¢¢–×ÆVÖVçFF–öâFW6–và ¢2222õÓb(	B¤4E2FW7F–æF–öâöÆ–7’„”â$Ud”Ur ¢Ò¢¤ö'6W'fVBWf–FVæ6S¢¢¢öæR6æ—F—¦VB&W6öÇfW"&W7öç6RW6VBâ…EE2u26Æ÷VDg&öçB6–væVBU$Âv—F‚F–ÖRÖÆ–Ö—FVB6–væGW&RVW'’&ÖWFW'2à¢Ò¢¤FV6—6–öâ7F–ÆÂ&WV—&VC¢¢¢v†–6‚W†7B6Æ÷VDg&öçB†÷7FæÖW2æB&VF—&V7BGFW&ç26âÆVv—F–ÖFRFV×÷&'’U$Ç2W6R7&÷72F†RFVæçBw26¶vR–çfVçF÷'“ð¢Ò¢¥&V6öÖÖVæFVBFVfVÇC¢¢¢6öæf–wW&RW†7Bö'6W'fVB†÷7FæÖW2BFWÆ÷–ÖVçBF–ÖRÂ6öÆÆV7BÖ÷&R6æ—F—¦VB6×ÆW2Â&WfÆ–FFRV6‚&VF—&V7BæB&V¦V7Bv–ÆF6&B6Æ÷VDg&öçBÂæöâÔ…EE2æB&—fFRFW7F–æF–öç2à¢Ò¢¥&WV—&VB'“¢¢¢6V7W&—G’FW6–và ¢2222õÓr(	B7F÷&R&WFVçF–öâæB6—¦R„”ÕÄTÔTåDTC²44UDä4RTäD”är ¢Ò¢¥6VÆV7FVBF—&V7F–öã¢¢¢Æâ&÷†–ÖFVÇ’S(	3ct"W6&ÆR66†RöâF†RD"Ö2âFVfVÇBFò6öæf–wW&&ÆR“ÖF’–æ7F—f—G’v–æF÷rÂG&–vvW"6ÆVçW&VÆ÷r3W&6VçBg&VR76RÂFVÆWFRöÆFW7B–æ7F—fR6ö×ÆWFVB6¶vW2f—'7BæB7F÷B3RW&6VçBg&VR76Rà¢Ò¢¤–×ÆVÖVçFF–öã¢¢¢†&FVæVB66†RÖÖ–çF–æW&Ö–çF–ç2&W7G&–7FVBÆ7BÖ66W72–æFW‚g&öÒ–çFW&æÂ7V66W76gVÂ×&WVW7BWfVçG2æB&VÖ÷fW2öæÇ’fÆ–FFVB&VwVÆ"f–æÂç¶vf–ÆW2âf–ÆW7—7FVÒF–ÖV—2æ÷BWF†÷&—FF—fRâF†R†VÇW"&V¦V7G2æWrf–ÆÇ2&VÆ÷rF†R6ÖR3×W&6VçBfÆö÷"à¢Ò¢¥&VÖ–æ–ær66WFæ6S¢¢¢W†W&6—6R6öçG&öÆÆVBF—6²&W77W&RæB6öæf—&Ò–æFW‚W'6—7FVæ6RÂFVÆWF–öâ÷&FW"Â&W7G&–7FVBVF—BæB6ÆVâ&VgW6Âv†VâæòVÆ–v–&ÆRf–ÆR6â&W7F÷&RF†RfÆö÷"à¢Ò¢¥&WV—&VB'“¢¢¢FWF–ÆVBFW6–và ¢2222õÓ‚(	BDÅ2÷væW'6†—…$U4ôÅdTBdõ"”ÄõB ¢Ò¢¤FV6—6–öã¢¢¢W6R¦6G2Ö66†Ræg'V—Bæ6†²7&VFRDå2&V6÷&G2ÖçVÆÇ’æBö'F–âF†R–æ—F–ÂV&Æ–6Ç’G'W7FVB6W'F–f–6FRF‡&÷Vv‚ÖçVÂDå2fÆ–FF–öâà¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢76–vâ6W'F–f–6FR÷væW"ÂÆW'BBÆV7B3F—2&Vf÷&RW‡—'’æB–çG&öGV6RVæGFVæFVB&VæWvÂ&Vf÷&R&öGV7F–öâ&÷fÂâÖçVÂ&VæWvÂ—266WF&ÆRöæÇ’f÷"F†R6öçG&öÆÆVB–Æ÷Bà ¢2222õÓ’(	B6V7&WBFVÆ—fW'’…$U4ôÅdTB ¢Ò¢¤FV6—6–öã¢¢¢7F÷&RF†R6V7&WB2âVçf—&öæÖVçB76–væÖVçB–â&ö÷BÖ÷væVBÖöFRÖc†÷7Bf–ÆR÷WG6–FRv—BæBÆWBFö6¶W"–æ¦V7B—BöæÇ’–çFòF†R†VÇW"6öçF–æW"à¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢W†W&6—6R&÷FF–öâF‡&÷Vv‚f–ÆR&WÆ6VÖVçBæB†VÇW"&V7&VF–öã²&W7G&–7BFö6¶W"FÖ–æ—7G&F–öâ&V6W6R&—f–ÆVvVB÷W&F÷'26â–ç7V7B6öçF–æW"Vçf—&öæÖVçBfÇVW2à ¢2222õÓ(	BÖöæ—F÷&–ærÆFf÷&Ò„õTâ ¢Ò¢¤FV6—6–öâ&WV—&VC¢¢¢v†–6‚ÆörÂÖWG&–2æBÆW'BÆFf÷&Òv–ÆÂ÷vâF†R6W'f–6RFVÆVÖWG'“ð¢Ò¢¥&V6öÖÖVæFVBFVfVÇC¢¢¢–çFVw&FRv—F‚F†RW†—7F–ærVçFW'&—6RÆFf÷&ÒæBW‡÷6R&öÖWF†WW2Ö6ö×F–&ÆRÖWG&–72–bF†BÖF6†W27W'&VçB7FæF&G2à¢Ò¢¥&WV—&VB'“¢¢¢÷W&F–öç2&VF–æW70 ¢2222õÓ(	BF‚ÖöFVÂ…$U4ôÅdTB ¢Ò¢¤FV6—6–öã¢¢¢F†Rf—'7B&VÆV6R66WG2W†7FÇ’öæR6æöæ–6Âf–ÆVæÖR6VvÖVçBVæF–ær–âÆ÷vW&66Rç¶và¢Ò¢¤W†6ÇVFVC¢¢¢æW7FVBF‡2ÂFF—F–öæÂf–ÆRG—W2ÂWW&66Rå´vÂ6Æ–VçB×7WÆ–VBU$Ç2æBVW'’×6VÆV7FVBFW7F–æF–öç2à¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢&WF–â÷6—F—fRæBæVvF—fRfÆ–FF–öâFW7G3²ç’'&öFW"æÖW76R&WV—&W2â&÷fVB66÷RæBF‡&VBÖÖöFVÂWFFRà ¢2222õÓ"(	B–çFVw&—G’ÖWFFF…$U4ôÅdTB ¢Ò¢¤ö'6W'fVBWf–FVæ6S¢¢¢FW&V6FVBtUBö’÷cö¦6G2öf–ÆW6&WGW&ç2f–ÆTæÖVÂÆVæwF†ÂÖCVÂ&Vv–öææB#‚Ö6†&7FW"†W†FV6–ÖÂ6†6fÇVRf÷"V6‚6¶vRà¢Ò¢¤FV6—6–öã¢¢¢&WV—&RW†7BÆVæwF†æB4„2ÓS"ÖF6‚&Vf÷&RFöÖ–2V&Æ–6F–öââÔCR—2æöâÖWF†÷&—FF—fRæB&WF–æVBöæÇ’f÷"–çFW&÷W&&–Æ—G’÷"F–væ÷7F–72à¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢&öGV7F–öâ6öçG&7BæB6†V6·7VÒÖÖ—6ÖF6‚66WFæ6RFW7@ ¢2222õÓ2(	B6FÆör&W7öç6R6†R…$U4ôÅdTB ¢Ò¢¤ö'6W'fVBWf–FVæ6S¢¢¢F†R6ö×ÆWFRtUBö’÷cö¦6G2öf–ÆW6&W7öç6R&Vv–ç2v—F‚¶æB—2F÷ÖÆWfVÂ¥4ôâ'&’öbÖWFFFVçG&–W2v—F†÷WBv–æF–öâVçfVÆ÷Rà¢Ò¢¤FV6—6–öã¢¢¢'6RF†Rö'6W'fVB6ö×ÆWFR'&’â–bgWGW&R&W7öç6RW6W2âVçfVÆ÷RF†B&W÷'G2Ö÷&R&V6÷&G2F†â—B6öçF–ç2Âf–ÂW‡Æ–6—FÇ’–ç7FVBöb&WGW&æ–ærfÇ6Ræ÷BÖf÷VæB&W7VÇBà¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢&WF–â6öçG&7BFW7G2æBÖöæ—F÷"F†RFW&V6FVBVæGö–çBf÷"66†VÖ6†ævW2à ¢2222õÓB(	B&öGV7F–öâÖ2†&Gv&R…$U4ôÅdTB ¢Ò¢¤FV6—6–öã¢¢¢W6RFVF–6FVBv—&VBÖ2Ö–æ’v—F‚#Bt"$ÒæBD"e27F÷&vRà¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢6öæf—&ÒF†RÆR6–Æ–6öâvVæW&F–öâæB&÷fRF†BF†R6VÆV7FVBFö6¶W"F—6²6—¦–ær7W÷'G2F†Rv÷&¶–ær6WBv†–ÆR&WF–æ–ær3W&6VçB6¶vR×7F÷&Rg&VR76RgFW"–ÖvW2ÂÆöw2æBFV×÷&'’F÷væÆöG2à ¢2222õÓR(	BFö6¶W"FW6·F÷Æ–6Vç6–ær…$U4ôÅdTB ¢Ò¢¤FV6—6–öã¢¢¢W6RFö6¶W"FW6·F÷VæFW"F†R÷&væ—¦F–öâÖ&÷fVB–BVçF—FÆVÖVçBæ÷rf–Æ&ÆRf÷"F†—2&öGV7F–öâv÷&¶ÆöBà¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢&V6÷&BF†R7V'67&—F–öâ÷væW"Â76–væVB66÷VçBö÷&væ—¦F–öâÂ&VæWvÂ&ö6W72æB7W÷'B6öçF7BâWFFR÷væW'6†—&VÖ–ç2VæFW"õÓ’à ¢2222õÓb(	BVæGFVæFVB7F'GWæB6W76–öâÖöFVÂ„”â$Ud”Ur ¢Ò¢¤FV6—6–öã¢¢¢W6RFVF–6FVBÖ4õ266÷VçBâf–ÆUfVÇBöÆöv–â&V†f–÷"—26öæf—&ÖVB2†æFÆVBâW6RÖævVBW"×W6W"ÆVæ6„vVçN(	Fæ÷Bæ÷&ÖÂ&ö÷BÆVæ6„FVÖöî(	GFò7F'BFö6¶W"FW6·F÷–âF†RuT’6W76–öâÂv—Bf÷"Fö6¶W"–æföÂ&V6öæ6–ÆR6ö×÷6RæBfÆ–FFRG'W7FVB…EE2&VF–æW72à¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢–×ÆVÖVçBF†R–FV×÷FVçB6öçG&öÆÆW"æBFVÖöç7G&FR÷vW"Ööâ×FòÖ†VÇF‡’&V6÷fW'’ÇW2f–ÇW&RÆW'F–ærv—F†÷WBÖçVÂ–çFW'fVçF–öââF†RÆ—fR&V&ö÷BFW7B—2W‡Æ–6—FÇ’FVfW'&VBà ¢2222õÓr(	B&öGV7F–öâ7F÷&vR&6¶–æræBÖ4õ2f—6–&–Æ—G’…$U4ôÅdTB ¢Ò¢¤FV6—6–öã¢¢¢W6RFö6¶W"æÖVBföÇVÖRB÷7'bö¦Öb×7F÷&VÂ&6¶VB'’Fö6¶W"FW6·F÷w2F—6²–ÖvRöâÖævVBe27F÷&vRâ&÷f–FR6¶vR–çfVçF÷'’æBÖævVÖVçBg&öÒÖ4õ2F‡&÷Vv‚Fö6¶W"÷"W'÷6RÖ'V–ÇBFÖ–æ—7G&F—fR6öÖÖæG2à¢Ò¢¤6Æ&–f–6F–öã¢¢¢Fö6¶W"öFÖ–æ—7G&F—fR66W72g&öÒÖ4õ26F—6f–W2F†Rf—6–&–Æ—G’&WV—&VÖVçC²æF—fRf–æFW"'&÷w6–ær—2æ÷B&WV—&VBà¢Ò¢¥fÆ–FFVBWf–FVæ6S¢¢¢&VÂf–ÆÂÂFöÖ–2V&Æ–6F–öâÂÆö6Â'—FR–FVçF—G’ÂÆö6Â&ævRFVÆ—fW'’æBW'6—7FVæ6R7&÷72†VÇW"ôät”å‚&W7F'B76VBöâF†RF&vWBÖ2à¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢VÆ–g’F—6²6—¦–ærÂFöÖ–2&VæÖRÂW&Ö—76–öç2Â–çFW''WF–öâÂ&W7F'BÂ&V&ö÷BÂWFFRæB&V6÷fW'“²&WF–âF†R3W&6VçB6¶vR×7F÷&Rg&VR×76RfÆö÷"à ¢2222õÓ‚(	Bät”å‚66W72Væf÷&6VÖVçBæB6Æ–VçBÖFG&W72f—6–&–Æ—G’…$U4ôÅdTB ¢Ò¢¤ö'6W'fVBWf–FVæ6S¢¢¢Äâ&WVW7Bg&öÒÖævVB6Æ–VçB&V6†VBät”å‚2Fö6¶W"FW6·F÷vFWv’“"ãc‚ãcRãÂæ÷B—G2&VÂ6÷W&6RFG&W72â&V6W6RF†RvFWv’—G6VÆbÖF6†VBF†Rf÷&ÖW"ófÆÆ÷vÆ—7BÂät”å‚6÷VÆBæ÷BF—7F–æwV—6‚&÷fVBæBVæ&÷fVB6Æ–VçG2à¢Ò¢¤FV6—6–öã¢¢¢&VÖ÷fR6÷W&6RÔ4”E"f–ÇFW&–æræB6öçF–çVRv—F†÷WB†÷7Bf—&WvÆÂ÷"6Æ–VçBWF†VçF–6F–öââ66WB66W72'’ç’6Æ–VçB&ÆRFò&÷WFRFòF†RÆ—7FVæW"à ¢2222õÓ’(	BFö6¶W"FW6·F÷&W6÷W&6RæBWFFRöÆ–7’„õTâ ¢Ò¢¤÷væW"FV6—6–öã¢¢¢F†R6W'f–6R÷væW"v–ÆÂ6öæf–wW&R5RÂ$ÒÂ7vÂF—6²Ö–ÖvRÆ–Ö—BÂ&W6÷W&6R6fW"ÂWFöÖF–2×WFFRæBÖ–çFVææ6R×v–æF÷r6WGF–æw2à¢Ò¢¥&V6öÖÖVæFVBFVfVÇC¢¢¢F—6&ÆR&W6÷W&6R6fW"ÂÆÆö6FRf—†VB&W6÷W&6W2Â&WfVçBVæ6öçG&öÆÆVB&öGV7F–öâWFFW2æBfÆ–FFR†VÇF‚gFW"WfW'’Ö4õ2÷"Fö6¶W"FW6·F÷WFFRà¢Ò¢¥&WV—&VB'“¢¢¢÷W&F–öç2&VF–æW70 ¢2222õÓ#(	B66†R&V6÷fW'’æB&6·W„õTâ ¢Ò¢¥6VÆV7FVBF—&V7F–öã¢¢¢G&VB6¶vW22&V'V–ÆF&ÆRFW&—fVBFFv—F†÷WB6¶vR×föÇVÖR&6·Wâ&÷FV7B6öæf–wW&F–öâÂ6W'F–f–6FW2Â&÷fVBFWÆ÷–ÖVçB&Wf—6–öâæB'Væ&öö·2÷WG6–FRF†RföÇVÖRà¢Ò¢¥&WV—&VBföÆÆ÷r×W¢¢¢W†W&6—6RFVÆ–&W&FRV×G’×föÇVÖR&V6÷fW'“¢&V7&VFRF†R6ö×÷6RföÇVÖRæBF—&V7F÷'’W&Ö—76–öç2ÂfÆ–FFRDÅ2ÂW&f÷&Ò&VÂf–ÆÂæB&W÷VÆFRöâFVÖæBâ7W7V7FVB6÷''WF–öâ&WV—&W2F–væ÷7F–72æBW‡Æ–6—B&÷fÂ&Vf÷&RföÇVÖRFVÆWF–öâà¢Ò¢¥&WV—&VB'“¢¢¢&V6÷fW'’FW6–và ¢2222õÓ#(	B&öGV7F–öâ†VÇW"–FVçF—G’…$U4ôÅdTB ¢Ò¢¤FV6—6–öã¢¢¢&VfW"æöâ×&ö÷B†VÇW"âW&Ö—BT”BöæÇ’gFW"W‡Æ–6—B&÷fÂÂv—F‚6öG&÷¢ÄÆÂæòÖæWr×&—f–ÆVvW6Â&VBÖöæÇ’&ö÷Bf–ÆW7—7FVÒæBöæÇ’F†R6¶vRföÇVÖRw&—F&ÆRà¢Ò¢¤–×ÆVÖVçFF–öã¢¢¢F†RÖ4õ2&öGV7F–öâ&öf–ÆRW6W2T”BcSS3&v—F‚&–Ö'’t”B²7F÷&RÖ–æ—F7&VFW2w&÷W×w&—F&ÆRæÖVB×föÇVÖRF—&V7F÷&–W2v—F†÷WB7&÷72ÕT”B6†÷væâÆÂ†VÇW"6&–Æ—F–W2&VÖ–âG&÷VBà¢Ò¢¥fÆ–FFVBWf–FVæ6S¢¢¢F†RF&vWBÖ27F'FVBF†R†VÇW"†VÇF‡’VæFW"F†—2–FVçF—G’Â6ö×ÆWFVB&VÂ¤4E2f–ÆÂÂFöÖ–6ÆÇ’V&Æ—6†VBF†R6¶vRÂ7W'f—fVB6W'f–ærÖ6öçF–æW"&W7F'BæB&V6÷fW&VBgFW"â–çFVçF–öæÂ†VÇW"7F÷âæòT”BÓW†6WF–öâ—2&WV—&VBà ¢222bã6öæf—&ÖVBFV6—6–öç0 §Â¢¤”B¢¢Â¢¤FV6—6–öâ¢¢Â¢¤6öæf—&ÖVBfÇVR¢¢À§ÂÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§ÂBÓÂFVÆ—fW'’7FvRÂ&öGV7F–öâ7—7FVÒâÀ§ÂBÓ"Â6¶vR–FVçF—G’Âf–ÆVæÖW2&R–Ö×WF&ÆS²6÷'&V7FVBfW'6–öç2&V6V—fRæWræÖW2âÀ§ÂBÓ2Â–æ—F–ÂFWÆ÷–ÖVçBÂät”å‚æB†VÇW"6öçF–æW'2F‡&÷Vv‚Fö6¶W"FW6·F÷öâöæRFVF–6FVBÖævVBÖ2âÀ§ÂBÓBÂ¦Öb’Æ–fV7–6ÆRÂW6RF†RFW&V6FVB¤4E2&W6öÇfW"æB6FÆörVæGö–çG2VçF–Â¦Öb–çG&öGV6W2&WÆ6VÖVçG3²¶VW&÷F‚&V†–æB&WÆ6V&ÆRFFW'2âÀ§ÂBÓRÂV&Æ–6F–öâ–çFVw&—G’Â&WV—&R6FÆörÆVæwF‚æB4„2ÓS"fW&–f–6F–öâ&Vf÷&RFöÖ–2V&Æ–6F–öã²Fòæ÷BG&VBÔCR2F†R6V7W&—G’&÷VæF'’âÀ§ÂBÓrÂ7F÷&vR&W&W6VçFF–öâÂ6ö×ÆWFVB6¶vW2W6R‡VÖâ×&VF&ÆRf–ÆW7—7FVÒF‚ÖF6†–ærF†R6æöæ–6Â6Æ–VçBU$ÂæB÷&–v–æÂf–ÆVæÖS²÷VR†6†VB&÷‡’Ö66†R7F÷&vR—2æ÷BW6VBâÀ§ÂBÓ‚ÂcF‚66÷RÂ66WBW†7FÇ’öæRfÆBf–ÆVæÖR6VvÖVçBVæF–ær–âÆ÷vW&66Rç¶v²æW7FVBF‡2æB÷F†W"f–ÆRG—W2&RW†6ÇVFVBâÀ§ÂBÓ’Â–æ—F–Â66ÆRÂFW6–vâf÷"S(	3"ÃÖævVBÖ72æBâ&÷†–ÖFVÇ’S(	3ct"6¶vRv÷&¶–ær6WBv†–ÆR&W6W'f–ærBÆV7B3W&6VçB6¶vR×7F÷&Rg&VR76RâÀ§ÂBÓÂ†÷7B&öf–ÆRÂFVF–6FVBv—&VBÖ2Ö–æ’v—F‚#Bt"$ÒÂD"e27F÷&vRÂFö6¶W"FW6·F÷æBFVF–6FVB6W'f–6R66÷VçC²WFöÖF–2&V6÷fW'’&VÖ–ç2Fò&RFVÖöç7G&FVBâÀ§ÂBÓRÂÖ4õ27F÷&vRÂW6RFö6¶W"æÖVBföÇVÖR–âFö6¶W"FW6·F÷w2e2Ö&6¶VBdÒF—6²–ÖvS²Fö6¶W"öFÖ–æ—7G&F—fR66W72g&öÒÖ4õ26F—6f–W2F†Rf—6–&–Æ—G’&WV—&VÖVçBâÀ§ÂBÓbÂ'VçF–ÖR–FVçF—G’ÂW6RfÆ–FFVBT”BcSS3&Â&–Ö'’t”BÂÆÂ6&–Æ—F–W2G&÷VBæB&VBÖöæÇ’&ö÷Bf–ÆW7—7FVÓ²æòT”BÓW†6WF–öâ—2&WV—&VBâÀ§ÂBÓÂ6W'f–6R66W72ÂV&Æ—6‚¦6G2Ö66†Ræg'V—Bæ6ƒ£ƒCC6v—F‚6W'fW"ÖWF†VçF–6FVBDÅ2æBæò6÷W&6RÔ4”E"f–ÇFW&–ær÷"6Æ–VçBWF†VçF–6F–öââç’&÷WFR×&V6†&ÆR6Æ–VçBÖ’&WVW7B6¶vW2âÀ§ÂBÓ"Â6W'F–f–6FRÂW6RÖçVÂDå2fÆ–FF–öâf÷"F†R–Æ÷Bv—F‚ÖæFF÷'’W‡—'’ÆW'F–æs²VæGFVæFVB&VæWvÂ&VÖ–ç2&öGV7F–öâvFRâÀ§ÂBÓ2Â6V7&WBFVÆ—fW'’Â–æ¦V7BF†R¦Öb6Æ–VçB6V7&WB–çFòF†R†VÇW"g&öÒ&ö÷BÖ÷væVBÖöFRÖc†÷7BVçf—&öæÖVçBf–ÆR÷WG6–FRv—BâÀ§ÂBÓBÂ÷WF&÷VæBæWGv÷&²ÂW6RF—&V7BfÆ–FFVB…EE2v—F†÷WBâ÷WF&÷VæB&÷‡’÷"DÅ2–ç7V7F–öââÀ ¢22râFVÆ—fW'’Æà §Â¢¥†6R¢¢Â¢¤Ö–â7F—f—F–W2¢¢Â¢¤W†—BWf–FVæ6R¢¢À§ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§ÂÂâ6öçG&7BfÆ–FF–öâÂ6öæf—&Ò¦Öb’&WÆ6VÖVçB÷&W7öç6RÂ6Æ–VçB…EE&V†f–÷W"Â¤4E2FöÖ–ç2Â’&—f–ÆVvRæBv÷&¶ÆöBFFâÂ&W6öÇfVBõÓ²6öæf—&ÖVB÷VÆF–öâ&ævS²ÖV7W&VBõÓ2ôõÓRôõÓbWf–FVæ6S²&VF7FVB’f—‡GW&W2âÀ§Â%ÂâFWF–ÆVBFW6–vâÂf–æÆ—¦R6ö×öæVçB6öçG&7BÂf–ÆW7—7FVÒ×7F÷&R÷&ævR7G&FVw’Â6ÆVçWöÆ–7’ÂæWGv÷&²öÆ–7’Â6V7&WG2ÂDÅ2ÂÖöæ—F÷&–ærÂ66—G’æB4ÄòâÂ&÷fVBFV6†æ–6ÂæB6V7W&—G’FW6–vââÀ§Â5Ââ'V–ÆBÂ–×ÆVÖVçBF†Rvò†VÇW"Âät”å‚6öæf–wW&F–öâÂ6öçF–æW"–ÖvW2ÂFWÆ÷–ÖVçBFVf–æ—F–öâÂÖWG&–72æB'Væ&öö²G&gBâÂfW'6–öæVBFWÆ÷–&ÆR'F–f7G2æBWFöÖFVBFW7G2âÀ§ÂEÂâ–çFVw&F–öâFW7BÂfÆ–FFR¦Öbô¤4E2–çFW&7F–öâÂ7G&VÖ–ærÂf–ÆW7—7FVÒÖ–ærÂÆö6Â×7F÷&R6VÖçF–72ÂÖ26Æ–VçB6ö×F–&–Æ—G’æBf–ÇW&R†æFÆ–ærâÂ66WFæ6RWf–FVæ6Rf÷"gVæ7F–öæÂ&WV—&VÖVçG2âÀ§ÂUÂâ6V7W&—G’æBÆöBFW7BÂW&f÷&ÒF‡&VBÖÖöFVÂfÆ–FF–öâÂ6V7&WBöÆör&Wf–WrÂ55$bFW7G2Â66—G’æB6öæ7W'&Væ7’FW7F–ærâÂ6V7W&—G’&÷fÂæBW&f÷&Öæ6R&W÷'BâÀ§ÂeÂâ&öGV7F–öâ&öÆÆ÷WBÂFWÆ÷’v—F‚6öçG&öÆÆVB6Æ–VçB66÷RÂÖöæ—F÷"&W7VÇG2ÂfÆ–FFR7W÷'B&ö6VGW&W2æBW‡æBgFW"F†Rö'6W'fF–öâW&–öBâÂ&öGV7F–öâ66WFæ6RæB6W'f–6R†æF÷fW"âÀ ¢22‚â&V6öÖÖVæFVBæW‡BFV6—6–öâ6WVVæ6P ¥&W6öÇfRF†R&VÖ–æ–ærVW7F–öç2–âF†—2÷&FW"&V6W6RV6‚ç7vW"6öç7G&–ç2F†RæW‡BÆ–W"öbFW6–vã  £â6GW&RF†R&VÖ–æ–ær6æ—F—¦VB¦ÖbWF†VçF–6F–öâÂF‡&÷GFÆRæB6W'fW"ÖW'&÷"6†W2à £"â6GW&R7GVÂ6Æ–VçBtUBô„TBõ&ævR&V†f–÷W"æBv÷&¶ÆöB66ÆR„õÓ2æBõÓR’à £2â6öæf—&ÒÆVv—F–ÖFR¤4E2ô4DâFW7F–æF–öç2æBfÆ–FFRF†R&W6öÇfVBõÓ"DÅ2ÖöæÇ’6W'f–6R&÷VæF'’„õÓb’à £Bâ6WBF†R4ÄòæB6ÆVçWöÆ–7’ÂF†Vâ6Æ÷6RF†R7F'GWÂ7F÷&vRVÆ–f–6F–öâÂFö6¶W"öÆ–7’æB&V6÷fW'’Wf–FVæ6R„õÓBÂõÓrÂõÓbÂõÓrÂõÓ’æBõÓ#’à £Râ76–vâ6W'F–f–6FR×&VæWvÂæBÖöæ—F÷&–ær÷væW'6†—ÂW†W&6—6R6V7&WB&÷FF–öâÂæB6Æ÷6RF†R÷W&F–öæÂföÆÆ÷r×W2f÷"õÓ‚FòõÓà £bâ&WF–â&Vw&W76–öâ6÷fW&vRf÷"F†R&W6öÇfVBF‚6öçG&7B„õÓ’æBfW&–g’F†R&W6öÇfVB–çFVw&—G’öÆ–7’„õÓ"’–â&öGV7F–öâÖÆ–¶RFW7G2à ¢22VæF—‚âW'&÷"†æFÆ–ærÖG&—€ §Â¢¤6öæF—F–öâ¢¢Â¢¤6Æ–VçB÷WF6öÖR¢¢Â¢¥7F÷&R7F–öâ¢¢Â¢¤F–væ÷7F–26FVv÷'’¢¢À§ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§Â–çfÆ–BÖWF†öB÷F‚öæÖRÂCóCBóCRÂ&V¦V7B&Vf÷&R†VÇW"Â6æ—F—¦VB6Æ–VçBW'&÷"À§Â6¶vR'6VçB–â¦ÖbÂCBÂFòæ÷B7&VFRf–æÂf–ÆRÂ&W6öÇfW%öæ÷Eöf÷VæBÀ§ÂôWF‚7&VFVçF–Ç2&V¦V7FVBÂS"ÂFòæ÷B&WG'’VçF–Â7&VFVçF–Ç2ö6öæf–wW&F–öâ6†ævRÂ¦ÖeöWF…öf–ÆVBÀ§Â¦Öb’F‡&÷GFÆVBÂS2²&WG'’ÔgFW"ÂFòæ÷B&WG'’v—F†–âF†R&WVW7BÂ¦Öe÷F‡&÷GFÆVBÀ§Â¦Öb&W6öÇfW"F–ÖV÷WBÂSBÂ&÷VæFVB&WG'’öæÇ’–b&÷fVBÂ¦Öe÷&W6öÇfW%÷F–ÖV÷WBÀ§Â¦Öbô¤4E2Væf–Æ&ÆR÷"W‡‚ÂS"Â&W6W'fRÆö6Â†—G3²f–ÂâVæ66†VB&WVW7BÂW7G&VÕ÷Væf–Æ&ÆRÀ§ÂÖÆf÷&ÖVB&W6öÇfW"¥4ôâÂS"ÂFòæ÷BföÆÆ÷rU$ÂÂ¦Öe÷&W7öç6Uö–çfÆ–BÀ§Â6FÆörÖ—76–æröGWÆ–6FRö–æ6ö×ÆWFWÂS"óCB'’6öæF—F–öâÂFòæ÷B&W6öÇfR÷"F÷væÆöBÂ¦Öeö6FÆöuö–çfÆ–BÀ§Â6–væVBU$Â&V¦V7FVBÂS"ÂFòæ÷B6öææV7BÂF÷væÆöE÷W&Å÷&V¦V7FVBÀ§Â¤4E2æ÷Bf÷VæBöW‡—&VBU$ÂÂS"óCB'’öÆ–7’ÂÖ’&W6öÇfRöæRæWrU$ÂæB&WG'’öæ6RÂ¦6G5öF÷væÆöEöf–ÆVBÀ§ÂW7G&VÒG&ç6fW"–çFW''WFVBÂW‡‚÷"7G&VÒ&W6WBÂF—66&B÷"V&çF–æR–æ6ö×ÆWFRFV×÷&'’f–ÆRÂW7G&VÕö–æ6ö×ÆWFRÀ§ÂÆVæwF‚÷"4„2ÓS"Ö—6ÖF6‚Â7G&VÒ&W6WBöÆövvVBf–ÇW&RÂF—66&BFV×÷&'’f–ÆS²æWfW"V&Æ—6‚f–æÂf–ÆRÂ6¶vUö–çFVw&—G•öf–ÆVBÀ§Â7F÷&Rw&—FRöF—6²gVÆÂÂSróW‡‚ÂFòæ÷BV&Æ—6‚f–æÂf–ÆS²ÆW'BÂ6¶vU÷7F÷&Uöf–ÆVBÀ§ÂÆö6Âf–ÆRf–Æ&ÆRÂW7G&VÒF÷vâÂ#ó#bÂ6W'fR–Ö×WF&ÆRf–æÂf–ÆRÂÆö6Åö†—BÀ ¢22VæF—‚"âÖ–æ–×VÒFVÆVÖWG' §Â¢¤&V¢¢Â¢¤Ö–æ–×VÒ6–væÇ2¢¢À§ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§Â&WVW7BÂ&WVW7B6÷VçBÂ7FGW2ÂGW&F–öâÂ'—FW2ÂÖWF†öBÂ6æ—F—¦VB6¶vRÆ&VÂÀ§Â6¶vR7F÷&RÂÄô4Âô¤4E26÷W&6RÂ†—G2ÂÖ—76W2Âf–æÂf–ÆW2ö'—FW2ÂFV×÷&'’'—FW2ÂÆö6²v—BÂ6ÆVçWæBFÖ–æ—7G&F—fR6†ævW2À§Â†VÇW"Â7F—fR7G&V×2Â6FÆör÷&W6öÇfW"ÆFVæ7’æB7FGW2Â–çFVw&—G’f–ÇW&W2ÂF÷væÆöBÆFVæ7’÷7FGW2Â&VF—&V7G2&V¦V7FVBÀ§ÂôWF‚Â&Vg&W6‚GFV×G2Â7V66W72öf–ÇW&RÂFö¶VâF–ÖR×FòÖW‡—'“²æWfW"Fö¶VâfÇVRÀ§Â7F÷&vRÂf–æÂ'—FW2öf–ÆW2Âg&VR'—FW2÷W&6VçBÂFV×÷&'’'—FW2ÂVç6fRö&¦V7G2æBV&Æ–6F–öâf–ÇW&W2À§Â'VçF–ÖRÂ6öçF–æW"†VÇF‚Â&W7F'G2Â5RÂÖVÖ÷'’ÂæWGv÷&²æB÷Vâ6öææV7F–öç2À ¢22VæF—‚2âvÆ÷76' §Â¢¥FW&Ò¢¢Â¢¤FVf–æ—F–öâ¢¢À§ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×ÂÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒÒ×À§ÂÆö6Â†—BÂ&W7öç6R6W'fVBg&öÒâÇ&VG’Ö6ö×ÆWFR&VwVÆ"f–ÆR–âF†RÆö6Â6¶vR7F÷&RâÀ§Â6¶vR×7F÷&Rf–ÆÂÂF†RW7G&VÒF÷væÆöBæBFöÖ–2V&Æ–6F–öâF†B7&VFW2æWr6ö×ÆWFRÆö6Â6¶vRf–ÆRâÀ§Â7F÷&RÖ—72Â&WVW7Bf÷"v†–6‚æò6ö×ÆWFR&VwVÆ"f–ÆRW†—7G2BF†R6æöæ–6ÂÆö6ÂF‚âÀ§Â¤4E2Â¦Öb6Æ÷VBF—7G&–'WF–öâ6W'f–6RâÀ§ÂVÆÂ×F‡&÷Vv‚6¶vR7F÷&RÂ‡VÖâ×&VF&ÆRÆö6Âf–ÆR7F÷&R÷VÆFVBWFöÖF–6ÆÇ’v†Vâ6Æ–VçBf—'7B&WVW7G2â'6VçB6¶vRâÀ§Â&W6öÇfW"ÂF†R¦Öb’6ÆÂF†BÖ26¶vRf–ÆVæÖRFòFV×÷&'’F÷væÆöBU$ÂâÀ§Â6–væVBU$ÂÂF–ÖRÖÆ–Ö—FVBU$Â6''––ærWF†÷&—¦F–öâFF–â—G2VW'’&ÖWFW'2÷"6–væGW&RâÀ§Â6–ævÆRÖfÆ–v‡BÂ6öÆW66–ær6–×VÇFæV÷W2÷W&F–öç26òöæRFö¶Vâ&Vg&W6‚÷"ö&¦V7BF÷væÆöB6W'fW2×VÇF—ÆRv—FW'2âÀ§Â55$bÂ6W'fW"×6–FR&WVW7Bf÷&vW'“¢Ö—7W6Röb6W'fW"Fò6öææV7BFòâGF6¶W"×6VÆV7FVBFW7F–æF–öââÀ ¢22VæF—‚BâWF†÷&—FF—fR&VfW&Væ6W0 ¥´¦Öc¢&WG&–WfRF÷væÆöBU$Âf÷"7V6–f–2¤4E2f–ÆUÒ†‡GG3¢òöFWfVÆ÷W"æ¦Öbæ6öÒö¦Öb×&ò÷&VfW&Væ6RövWE÷cÖ¦6G2Öf–ÆW2Öf–ÆVæÖR ¥´¦Öc¢&WG&–WfRÆ—7Böb¤4E2f–ÆW2æBÖWFFFÒ†‡GG3¢òöFWfVÆ÷W"æ¦Öbæ6öÒö¦Öb×&ò÷&VfW&Væ6RövWE÷cÖ¦6G2Öf–ÆW2 ¥´¦Öc¢ö'F–ââ66W72Fö¶VâW6–ærâ’6Æ–VçEÒ†‡GG3¢òöFWfVÆ÷W"æ¦Öbæ6öÒö¦Öb×&ò÷&VfW&Væ6R÷÷7FöWF‡Fö¶Vâ ¥´¦Öc¢6Æ–VçB7&VFVçF–Ç5Ò†‡GG3¢òöFWfVÆ÷W"æ¦Öbæ6öÒö¦Öb×&òöFö72ö6Æ–VçBÖ7&VFVçF–Ç2 ¥´¦Öc¢&—f–ÆVvW2æBFW&V6F–öç5Ò†‡GG3¢òöFWfVÆ÷W"æ¦Öbæ6öÒö¦Öb×&òöFö72÷&—f–ÆVvW2ÖæBÖFW&V6F–öç2 ¥´¦Öc¢¤4E26öÖ×Væ–6F–öåÒ†‡GG3¢òöÆV&âæ¦Öbæ6öÒ÷"öVâÕU2÷FV6†æ–6ÂÖ'F–6ÆW2ô¦Öeô6Æ÷VEôF—7G&–'WF–öåõ6W'f–6Uô6öÖ×Væ–6F–öâ ¥´ät”åƒ¢G'•öf–ÆW2F—&V7F—fUÒ†‡GG3¢òöæv–ç‚æ÷&röVâöFö72ö‡GGöæw…ö‡GGö6÷&UöÖöGVÆRæ‡FÖÂ7G'•öf–ÆW2 ¢¥&VfW&Væ6R7FGW2&Wf–WvVB#rVwW7B##bâF†RF&vWB¦Öb&òFVæçBw2÷vâö’öFö2&VÖ–ç2WF†÷&—FF—fRf÷"VæGö–çBf–Æ&–Æ—G’æB66†VÖB–×ÆVÖVçFF–öâF–ÖRâ 