# macOS production-candidate validation — 31 August 2026

## Scope and disclosure boundary

This record captures sanitized evidence from the first controlled validation of
`deploy/macos-production/` on the target Docker Desktop Mac. It intentionally
excludes the package filename, Jamf tenant, JCDS/CDN hostname, OAuth values,
signed URL, private key and unsanitized helper logs.

This is engineering acceptance evidence, not final production approval.
Unattended reboot recovery, resource/update policy, Power Automate alert and
retention ownership, and certificate-renewal ownership remain open.

## Validated target

| Item | Evidence |
|---|---|
| Host | Dedicated Mac running licensed Docker Desktop |
| Profile | `deploy/macos-production/compose.yaml` plus the optional monitoring override for webhook acceptance |
| Listener | Trusted HTTPS on `jcds-cache.appfruit.ch:8443` |
| Certificate | DNS SAN matched; issued by `Post CH AG JSS Built-in Certificate Authority`; clients trust the issuer |
| Storage | Docker named volume `jcds-content-cache-macos-production_package-store` |
| Helper identity | UID `65532`, primary GID `0`, all capabilities dropped, read-only root filesystem |
| NGINX | Baked production configuration; TLS material mounted read-only |

## Results

| Test | Expected result | Observed result | Status |
|---|---|---|---|
| Compose initialization | `store-init` exits `0`; helper, maintainer and NGINX become healthy | Initializer exited `0`; all three long-running containers healthy | Passed |
| Trusted readiness | Client trusts certificate and receives ready response | HTTPS `200` with `{"status":"ready"}` | Passed |
| Real cache miss | Helper authenticates, resolves, streams, verifies and publishes | HTTPS `200` with `X-Package-Source: JCDS` | Passed |
| Concurrent full miss | A second full GET joins the active fill without waiting for final publication or starting an independent response class | Leader returned `JCDS`; concurrent follower returned `INFLIGHT` | Passed |
| In-flight byte equality | Leader and follower receive the same complete package bytes | `JCDS` and `INFLIGHT` outputs compared byte-for-byte | Passed |
| Fan-out publication | The shared fill publishes once and becomes a normal local hit | Follow-up returned `LOCAL` and matched the leader byte-for-byte | Passed |
| Local cache hit | Second request avoids upstream and returns identical bytes | HTTPS `200` with `X-Package-Source: LOCAL`; byte comparison succeeded | Passed |
| Local byte range | Cached package supports resume semantics | HTTPS `206`; first 1,024 bytes matched the complete file | Passed |
| Serving-container restart | Named-volume package survives helper/NGINX restart | HTTPS `200`, `LOCAL`; byte comparison succeeded after restart | Passed |
| Helper unavailable — local hit | NGINX continues serving completed packages | Cached package remained available as `LOCAL` | Passed |
| Helper unavailable — miss | Missing package fails without publishing a partial file | Controlled `502` response | Passed |
| Helper recovery | Helper becomes healthy after restart | Both long-running containers returned to healthy state | Passed |
| Non-root volume compatibility | Helper starts and publishes without owning/chmodding the named-volume directories | Target Mac validated UID `65532`:GID `0` model after the EPERM compatibility fix | Passed |
| Jamf path compatibility | `/Packages/` and `/packages/` address one canonical object | Uppercase miss returned `JCDS`; lowercase follow-up returned `LOCAL`; responses were byte-identical | Passed |
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
4. `50b2656` added one-transfer live fan-out from the growing private temporary
   file, the `INFLIGHT` source class and the Range publication gate.

The automated suite covers Go formatting, race tests, vet, binaries, images,
mock end-to-end behavior, the localhost real-backend profile, production
configuration, the actual non-root helper startup path, named-volume atomic
publication, hardened NGINX syntax and protected TLS-key access. Its deployed
Compose test additionally proves one upstream object request for byte-identical
`JCDS` and `INFLIGHT` responses. The target-Mac validation independently proved
the same response classes and byte equality against the real backend; package
identity, size and request IDs remain intentionally excluded from this record.

## Cache-lifecycle acceptance

The configurable cache-maintainer was subsequently exercised on the production
Mac with two disposable Docker volumes. The real package and maintenance
volumes were not mounted into the test.

| Check | Sanitized result |
|---|---|
| Runtime configuration | 90-day retention, 30% trigger and 35% target loaded |
| Access index | Successful real cache hit produced a mode-`0600`, UID 65532/GID 0 index |
| Index persistence | Index hash remained identical across a maintainer-container restart |
| Eligible synthetic file | Old regular `.pkg` removed |
| Ineligible synthetic file | Recent regular `.pkg` preserved |
| Symlink safety | Synthetic `.pkg` symlink preserved |
| Deletion audit | Restricted mode-`0600`, UID 65532/GID 0 audit created |
| Test isolation | Disposable volumes removed after the test |

The one-second acceptance interval intentionally caused later cleanup passes
after the eligible file had already been removed. A later `removed_files=0`
record therefore indicated an exhausted candidate set, not a failed deletion.
Production retains the 15-minute default interval. The maintainer now reports
whether cleanup actually restored its configured free-space target.

## Empty-volume recovery acceptance

Recovery was exercised through an isolated Compose project on port `18444`.
The production project and its volumes remained mounted and healthy throughout.
Only resources carrying the isolated recovery-project label were selected for
destruction.

| Check | Sanitized result |
|---|---|
| Isolation | Separate package and maintenance volumes created under an explicit recovery project |
| Recovery baseline | Empty volume filled from JCDS; follow-up response was LOCAL and byte-identical |
| Destructive target validation | Exactly two recovery volumes identified; production volumes excluded |
| Complete data loss | Both isolated volumes and all isolated containers/networks removed |
| Production continuity | Production readiness remained HTTPS `200` during the exercise |
| Empty rebuild | Compose recreated both volumes and all long-running services became healthy |
| Rehydration | Recreated cache returned `JCDS` then `LOCAL` for the same package |
| Integrity | Rehydrated bytes matched the private pre-deletion SHA-256 baseline |
| Cleanup | Isolated recovery project and its volumes removed after acceptance |

Package bytes are therefore accepted as derived, rebuildable data for the
pilot. Configuration, TLS material, private environment files, the approved
repository revision and runbooks remain the authoritative recovery inputs and
must be protected independently.

## Webhook monitoring acceptance — 1 September 2026

The optional monitoring override was enabled on a newly installed production
Mac and delivered its versioned JSON snapshot to the approved Power Automate
HTTPS receiver. The private signed trigger URL was not recorded.

| Check | Sanitized result |
|---|---|
| Delivery | Power Automate received schema version 1 JSON |
| Authentication mode | No additional HMAC; signed HTTPS trigger URL treated as a protected bearer credential |
| Stable identity | Operator name, FQDN and persisted UUID present |
| Readiness | `ready=true`, gateway status `200` |
| TLS | Expected subject and expiry parsed; status `ok` |
| Capacity | Total, available and free-percent fields present; pressure false |
| Cache | Full inventory mode active; empty new cache reported consistently |
| Lifecycle | 90-day retention and 30/35-percent cleanup thresholds reported |
| Privacy | No Jamf credential, signed URL, package URL, client address or request ID included |

The first snapshot correctly reported sequence `1`, near-zero uptime and no
previous delivery. Those startup values are defined behavior, not monitoring
failure. Application version and commit are optional metadata and may remain
`unknown` without affecting acceptance.

## Deferred and remaining gates

- OQ-16 unattended reboot/session recovery: explicitly deferred.
- OQ-17: final disk sizing, full macOS reboot and Docker Desktop update behavior
  remain; named-volume write, restart persistence and destructive recovery passed.
- OQ-19 Docker Desktop CPU, memory, disk, Resource Saver and update policy.
- Observe cache retention and low-disk behavior during the pilot; the isolated
  target-Mac acceptance test passed.
- Power Automate alert routing/retention ownership and certificate-renewal automation.
- Actual managed-client `HEAD`, resume and multi-range behavior under OQ-05.
