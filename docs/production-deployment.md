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

## Remaining production gates

Production-pilot approval still requires closure of:

1. Demonstrate unattended restart/session recovery from Mac power-on to healthy HTTPS.
2. Record final Docker Desktop CPU, memory, disk-image, Resource Saver and update policy.
3. Qualify final named-volume capacity, representative large-file performance,
   macOS reboot and Docker Desktop update behavior.
4. Assign certificate-renewal ownership and implement unattended renewal before
   final production approval.
5. Assign Power Automate receiver, alert, retention, escalation and signed-URL
   rotation ownership, plus the availability SLO.

Hardware, helper identity, named-volume visibility, cache retention, rebuild
policy, trusted TLS and target-Mac webhook delivery are resolved for the pilot.

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

The named-volume model is accepted for the pilot. Completed target-Mac tests
prove same-filesystem publication, cross-container permissions, serving-
container restart persistence, administrative inspection through Docker and
destructive empty-volume recovery. Final production approval still requires:

- sustained large-file throughput;
- correct Docker Desktop disk-image sizing and APFS free-space monitoring;
- behaviour across Docker Desktop and macOS updates;
- full Mac reboot persistence; and
- representative capacity and concurrent-load qualification.

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

The target-Mac acceptance exercise passed using an explicitly isolated Compose
project and two labeled disposable volumes. After complete volume deletion,
Compose recreated the empty service, a real package was rehydrated from JCDS,
the subsequent request was local and the bytes matched the private pre-deletion
hash. The production service remained ready throughout. Package backups are not
required for the pilot; protect configuration, TLS material, private environment
files, the approved revision and runbooks instead.

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
macOS directory and mounted read-only into NGINX. Manual DNS validation remains
acceptable for the controlled pilot only when:

- a certificate-renewal owner is named;
- expiry is monitored with at least 30 days warning;
- renewal and NGINX reload are tested;
- unattended renewal is planned before general production approval.

## Network exposure

The production listener binds TCP 8443 to the approved host interface. The
localhost test profile must remain bound to `127.0.0.1`.

No host firewall, source-CIDR filter or client authentication is used. A live
LAN request proved that Docker Desktop replaces the original client address
with gateway `192.168.65.1`, so NGINX cannot enforce the former source policy.
The service owner explicitly chose to remove that policy and accept requests
from any client able to route to TCP 8443.

Server-authenticated TLS protects confidentiality and server authenticity in
transit; it does not authorize the requesting client. A route-reachable client
that knows or guesses a package filename can retrieve a cached package or
trigger a JCDS fill. This exposure is an accepted service boundary and must be
reassessed if network reachability expands.

Only TCP 8443 may be published. The helper and plaintext health endpoint remain
inside the Docker network/container boundary. Outbound HTTPS is limited to the
Jamf tenant and exact approved JCDS/CDN hostnames, with every redirect
revalidated.

## Production profile requirements

`deploy/macos-production/` must provide:

- a baked, reviewed NGINX configuration to avoid fragile configuration bind
  mounts;
- TLS listener on 8443 and no plaintext LAN listener;
- private helper network with no published helper port;
- selected persistent storage implementation;
- read-only container root filesystems and `no-new-privileges`;
- all unnecessary capabilities dropped;
- approved helper UID model;
- CPU, memory, PID and logging bounds;
- health checks and restart policies;
- certificate and secret mounts/injection;
- capacity thresholds and temporary-file cleanup;
- architecture-compatible images for the selected Apple silicon host.

The initial candidate implements these controls. Its helper remains non-root at
UID `65532` with primary GID `0`, allowing writes to group-owned named-volume
directories without the cross-UID `chown` that Docker Desktop rejected during
integration testing. The helper retains `cap_drop: ALL`, `no-new-privileges`
and a read-only root filesystem. The target Mac has validated this model.

The target-Mac validation completed OQ-21: the helper became healthy as UID
`65532`/GID `0`, performed a real fill and atomic publication, survived serving
container restart and recovered after an intentional helper stop. See
[`macos-production-validation-2026-08-31.md`](macos-production-validation-2026-08-31.md)
for the sanitized evidence. Unattended Mac reboot recovery remains deferred.
The retained design for unattended startup, cache lifecycle and volume recovery
is defined in [`production-readiness-plan.md`](production-readiness-plan.md).

NGINX retains only `CHOWN`, `SETGID`, `SETUID` and `DAC_READ_SEARCH` after
dropping all capabilities. `DAC_READ_SEARCH` lets its root master read a
mode-`0600` Mac-owned TLS private key from the read-only certificate-directory
mount without granting discretionary write bypass; workers still switch to the
unprivileged `nginx` identity.

## Controlled candidate startup

Do not reuse the localhost real-backend environment file. On the target Mac,
create a private runtime directory outside the checkout, copy the two example
files, replace every `REPLACE` value, and restrict the helper environment:

```bash
runtime_root="${HOME}/JCDS-ContentCache-runtime"
mkdir -p "${runtime_root}/tls"

cp deploy/macos-production/cache-helper.env.example \
  "${runtime_root}/cache-helper.production.env"
cp deploy/macos-production/deployment.env.example \
  "${runtime_root}/deployment.production.env"

chmod 0600 "${runtime_root}/cache-helper.production.env"
chmod 0600 "${runtime_root}/deployment.production.env"
```

Place `fullchain.pem` and `privkey.pem` in the configured TLS directory and
keep the private key mode `0600`. After editing the two private environment
files, validate and start the candidate:

```bash
docker compose \
  --env-file "${runtime_root}/deployment.production.env" \
  --file deploy/macos-production/compose.yaml \
  config --quiet

docker compose \
  --env-file "${runtime_root}/deployment.production.env" \
  --file deploy/macos-production/compose.yaml \
  up --build --detach

docker compose \
  --env-file "${runtime_root}/deployment.production.env" \
  --file deploy/macos-production/compose.yaml \
  ps --all
```

Expected state: `store-init` exited `0`; `cache-helper`, `cache-maintainer` and
`nginx` are healthy.
Do not use `down --volumes` during normal restart or upgrade operations.

The two copied files remain the only private environment files used by this
profile. `cache-helper.production.env` contains Jamf/JCDS helper configuration.
`deployment.production.env` supplies Compose paths, lifecycle policy and the
optional webhook settings. Always pass the latter with `--env-file`; do not
create an additional monitoring environment file. The detailed reporter setup
and first/second-report acceptance criteria are documented in
[`webhook-monitoring.md`](webhook-monitoring.md).

The deployment environment also controls the cache lifecycle. The shipped
defaults retain packages for 90 days (`JCDS_CACHE_RETENTION=2160h`), start
cleanup below 30 percent free space and stop after recovering to 35 percent.
Edit the private deployment environment and recreate `cache-maintainer` to
change these values; no image rebuild is required. Keep the maintenance volume
because it contains the restricted last-access index and deletion audit.

LAN acceptance must confirm trusted TLS, health and package delivery. NGINX
monitoring will record Docker Desktop's gateway address rather than the original
client, so per-client attribution is unavailable at this layer.

## Monitoring and operations

Production monitoring must cover:

- HTTPS readiness and certificate expiry;
- Docker Desktop application/VM availability;
- container health and restart loops;
- local hits, upstream fills, status classes, latency and bytes;
- OAuth/Jamf/JCDS failures and redirect-policy rejections;
- active fills, integrity failures and temporary-file cleanup;
- package-store, Docker VM disk and host APFS capacity;
- cache-maintainer health, cleanup summaries and restricted deletion audit;
- macOS reboot/update and Docker Desktop update outcomes.

NGINX behavior records must retain the existing privacy boundary: no package
name, URI, query, raw range, raw user agent, credentials or signed URL.

The implemented optional monitoring integration extends the existing
cache-maintainer with one periodic snapshot collector. Independent consumers
can expose the latest snapshot without authentication at `/health/metrics`
and/or send the exact same JSON bytes to the HTTPS webhook. Both are disabled
by default and configured with the separate Compose override. The public API
inherits the accepted route-reachable TLS boundary and can disclose package
names when full inventory is selected. API and receiver failure must not affect delivery,
cleanup, readiness or container health. Operators must retain an external HTTPS
probe because the container cannot prove macOS/Docker Desktop health, actual
client-network reachability or which certificate NGINX serves. The reporter's
maintainer mount contains only the public certificate, never its private key.

## Pilot acceptance sequence

Before accepting concurrent delivery, use one approved package that is absent
from the cache and start two full GETs close together. The first must report
`JCDS`, the second must report `INFLIGHT`, both outputs must compare
byte-for-byte, and upstream telemetry must show one object transfer. Repeat
with a Range follower during a fresh fill: it must wait for atomic publication
and then return `206 LOCAL`. A deliberately interrupted isolated test must
leave no final package. Do not publish the test filename or unfiltered helper
logs.

1. Close the remaining evidence and policy gates in OQ-16, OQ-17 and OQ-19 and approve the architecture.
2. Review and qualify the implemented `deploy/macos-production/` candidate.
3. Build and test the selected ARM64 images in CI and on the target Mac.
4. Configure DNS, certificate, NGINX access policy, storage and protected secret delivery.
5. Prove cold-boot recovery and controlled update/restart behavior.
6. Validate allowed and denied LAN clients plus TLS trust.
7. Run real miss, local hit, range, restart, upstream-outage, disk-low and
   integrity-failure tests.
8. Measure throughput, time to first byte, CPU, RAM, disk I/O, Docker disk growth
   and WAN savings.
9. Pilot with a small managed-client group and agreed rollback criteria.
10. Obtain service-owner, security and operations approval before expansion.

## Rollback principle

Application rollback must recreate the prior reviewed images and configuration
without deleting the package volume. Platform rollback must separately cover
Docker Desktop and macOS updates. Never use `docker compose down --volumes` in
normal rollback or troubleshooting unless intentional cache deletion is
approved and clearly communicated.
