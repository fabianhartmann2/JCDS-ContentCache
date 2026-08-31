# macOS production deployment readiness

## Status and scope

The production target is a dedicated Mac running Docker Desktop. This document
defines the preparation and acceptance work for that target. It is not yet an
executable deployment runbook because the blocking decisions in
`docs/architecture.md` have not all been answered and
`deploy/macos-production/` has not been implemented.

The existing profiles have different purposes:

| Profile | Purpose | Production status |
|---|---|---|
| `deploy/compose/` | Credential-free mock development and CI | Not production |
| `deploy/macos/` | Localhost-only real Jamf/JCDS integration test | Not production; never expose to the LAN |
| `deploy/production/` | Earlier Ubuntu/Docker Engine candidate | Superseded; retained temporarily for reference |
| `deploy/macos-production/` | Future TLS-enabled Mac production profile | Not yet implemented |

## Confirmed service boundary

| Setting | Confirmed value |
|---|---|
| Host platform | Dedicated Mac running Docker Desktop |
| Service DNS | `jcds-cache.appfruit.ch` |
| Listener | HTTPS on TCP 8443 |
| Client network | `192.168.0.0/16` |
| Client authentication | CIDR restriction plus server-authenticated TLS for v1 |
| Workload | 500–2,000 managed Macs |
| Cache capacity | 500 GB–1 TB usable with at least 20 percent headroom |
| Container data paths | `/srv/jamf-store/packages` and `/srv/jamf-store/.temporary` |
| Outbound network | Direct validated HTTPS; no proxy or TLS inspection |
| Package integrity | Exact Jamf catalog length and SHA3-512 before atomic publication |

## Production blockers

Implementation of the production Compose profile requires answers for:

1. Mac model, Apple silicon generation, RAM, storage and network interface.
2. Docker Desktop subscription entitlement and operational owner.
3. Dedicated macOS account and unattended restart/session model.
4. APFS bind-mount qualification and capacity evidence.
5. NGINX source-address visibility and allowed/denied LAN evidence.
6. Production helper UID model for the selected storage implementation.

The pilot additionally requires Docker resource/update policy, cache recovery,
certificate-renewal ownership, monitoring ownership, retention and SLO.

## Recommended host baseline

The approved host baseline is:

- Dedicated wired Apple-silicon Mac mini.
- 24 GB RAM and 1 TB APFS storage.
- A Finder-visible APFS package directory with at least 20 percent operational
  headroom; actual usable cache capacity must account for macOS, images, logs
  and temporary in-progress downloads.
- Static address or DHCP reservation and stable DNS/time synchronization.
- Supported macOS release managed through MDM.
- Sleep and automatic power-off disabled for the always-on service.
- UPS-backed power where required by the agreed recovery objective.

Docker Desktop supports the current and two previous major macOS releases.
The service owner must keep the host within that support window:

- <https://docs.docker.com/desktop/setup/install/mac-install/>

## Docker Desktop governance

The user has confirmed that Docker Desktop's free tier applies to this
deployment. That eligibility must be recorded by the service owner and
rechecked if organizational size, revenue, ownership or usage changes. Docker
Desktop currently requires a paid subscription for professional use in larger
organizations. Settings management and updates still need named owners:

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

### Selected option — dedicated APFS bind mount

Production requires package files to be directly visible in Finder, so a
dedicated host APFS directory will be bind-mounted at `/srv/jamf-store`.
Docker-managed named volumes are not acceptable for this requirement because
their files are managed inside Docker Desktop rather than exposed as normal
host files.

The bind mount is selected but is not approved for pilot use until testing proves:

- sustained large-file throughput;
- same-filesystem atomic rename;
- stable permissions across containers and reboots;
- no `EIO` or file-sharing failures;
- behaviour across Docker Desktop and macOS updates;
- safe host-side pre-population and cleanup.

The qualification must use representative multi-gigabyte packages, concurrent
readers, an interrupted fill, container recreation, Docker Desktop restart,
macOS reboot and a controlled Docker Desktop update. It must also verify that
`.temporary` and `packages` reside on the same APFS filesystem and that only an
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
macOS directory and mounted read-only into NGINX. Manual DNS validation remains
acceptable for the controlled pilot only when:

- a certificate-renewal owner is named;
- expiry is monitored with at least 30 days warning;
- renewal and NGINX reload are tested;
- unattended renewal is planned before general production approval.

## Network and firewall controls

The production listener binds TCP 8443 to the approved host interface. The
localhost test profile must remain bound to `127.0.0.1`.

No host firewall is planned. NGINX is therefore the intended enforcement point
for `192.168.0.0/16`. Because Docker Desktop forwards traffic through its Linux
VM, this design is blocked until a LAN test proves NGINX receives a trustworthy
original source address. Test one allowed client and one client outside the
approved range, record NGINX's observed address, and prove the latter receives
a denial. If both appear as a Docker gateway or another shared address, NGINX
cannot implement the requirement and a source-aware host/perimeter control is
mandatory.

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

## Monitoring and operations

Production monitoring must cover:

- HTTPS readiness and certificate expiry;
- Docker Desktop application/VM availability;
- container health and restart loops;
- local hits, upstream fills, status classes, latency and bytes;
- OAuth/Jamf/JCDS failures and redirect-policy rejections;
- active fills, integrity failures and temporary-file cleanup;
- package-store, Docker VM disk and host APFS capacity;
- macOS reboot/update and Docker Desktop update outcomes.

NGINX behavior records must retain the existing privacy boundary: no package
name, URI, query, raw range, raw user agent, credentials or signed URL.

## Pilot acceptance sequence

1. Close the remaining evidence and policy gates in OQ-16 through OQ-21 and approve the architecture.
2. Implement and review `deploy/macos-production/`.
3. Build and test the selected ARM64 images in CI and on the target Mac.
4. Configure DNS, certificate, firewall, storage and protected secret delivery.
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
