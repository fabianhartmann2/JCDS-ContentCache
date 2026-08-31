# macOS production-candidate validation — 31 August 2026

## Scope and disclosure boundary

This record captures sanitized evidence from the first controlled validation of
`deploy/macos-production/` on the target Docker Desktop Mac. It intentionally
excludes the package filename, Jamf tenant, JCDS/CDN hostname, OAuth values,
signed URL, private key and unsanitized helper logs.

This is engineering acceptance evidence, not final production approval.
Unattended reboot recovery, resource/update policy, retention, monitoring and
certificate-renewal ownership remain open.

## Validated target

| Item | Evidence |
|---|---|
| Host | Dedicated Mac running licensed Docker Desktop |
| Profile | `deploy/macos-production/compose.yaml` |
| Listener | Trusted HTTPS on `jcds-cache.appfruit.ch:8443` |
| Certificate | DNS SAN matched; issued by `Post CH AG JSS Built-in Certificate Authority`; clients trust the issuer |
| Storage | Docker named volume `jcds-content-cache-macos-production_package-store` |
| Helper identity | UID `65532`, primary GID `0`, all capabilities dropped, read-only root filesystem |
| NGINX | Baked production configuration; TLS material mounted read-only |

## Results

| Test | Expected result | Observed result | Status |
|---|---|---|---|
| Compose initialization | `store-init` exits `0`; helper and NGINX become healthy | Initializer exited `0`; both long-running containers healthy | Passed |
| Trusted readiness | Client trusts certificate and receives ready response | HTTPS `200` with `{"status":"ready"}` | Passed |
| Real cache miss | Helper authenticates, resolves, streams, verifies and publishes | HTTPS `200` with `X-Package-Source: JCDS` | Passed |
| Local cache hit | Second request avoids upstream and returns identical bytes | HTTPS `200` with `X-Package-Source: LOCAL`; byte comparison succeeded | Passed |
| Local byte range | Cached package supports resume semantics | HTTPS `206`; first 1,024 bytes matched the complete file | Passed |
| Serving-container restart | Named-volume package survives helper/NGINX restart | HTTPS `200`, `LOCAL`; byte comparison succeeded after restart | Passed |
| Helper unavailable — local hit | NGINX continues serving completed packages | Cached package remained available as `LOCAL` | Passed |
| Helper unavailable — miss | Missing package fails without publishing a partial file | Controlled `502` response | Passed |
| Helper recovery | Helper becomes healthy after restart | Both long-running containers returned to healthy state | Passed |
| Non-root volume compatibility | Helper starts and publishes without owning/chmodding the named-volume directories | Target Mac validated UID `65532`:GID `0` model after the EPERM compatibility fix | Passed |
| Client-address visibility | Determine whether NGINX can enforce the former source CIDR | LAN client appeared as Docker Desktop gateway `192.168.65.1` | Failed by platform design |

## Access-policy outcome

The source-address test proved that Docker Desktop hides the original LAN
client from NGINX. The former `192.168.0.0/16` rule was ineffective because the
Docker gateway address itself matched that range. The service owner explicitly
selected the following replacement boundary:

- no host firewall;
- no source-CIDR filtering;
- no client authentication;
- server-authenticated TLS remains mandatory;
- any client able to route to TCP 8443 may request a known package filename.

Commit `746d7d5` removes the ineffective NGINX rule and records the accepted
exposure. CI prevents that specific source-CIDR rule from being reintroduced as
if it were effective behind Docker Desktop.

## Fixes validated during the session

1. `c8c527f` added the hardened macOS production candidate.
2. `8270941` allowed the non-root helper to accept safe, writable,
   pre-provisioned directories when Docker Desktop rejects its `chmod` with
   `EPERM`; unsafe private-directory modes and other errors remain fatal.
3. `746d7d5` removed ineffective source filtering after live source masking was
   observed and documented the TLS-only access boundary.

The automated suite covers Go formatting, race tests, vet, binaries, images,
mock end-to-end behavior, the localhost real-backend profile, production
configuration, the actual non-root helper startup path, named-volume atomic
publication, hardened NGINX syntax and protected TLS-key access.

## Deferred and remaining gates

- OQ-16 unattended reboot/session recovery: explicitly deferred.
- OQ-17: disk sizing, full macOS reboot, Docker Desktop update and destructive
  recovery remain; named-volume write, atomic publication and container restart
  persistence passed.
- OQ-19 Docker Desktop CPU, memory, disk, Resource Saver and update policy.
- OQ-20 cache backup/rebuild policy.
- Cache retention/cleanup and low-disk operational procedure.
- Monitoring/alert ownership and certificate-renewal automation.
- Actual managed-client `HEAD`, resume and multi-range behavior under OQ-05.
