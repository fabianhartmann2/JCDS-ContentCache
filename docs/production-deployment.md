# Production-candidate deployment

## Status and scope

This runbook deploys the first single-host production candidate for:

| Setting | Confirmed value |
|---|---|
| Operating system | Ubuntu Server 26.04 LTS, amd64/x86_64 |
| Service DNS name | `jcds-cache.appfruit.ch` |
| Client network | `192.168.0.0/16` |
| Client authentication | Network CIDR restriction for v1 |
| Listener | HTTPS on TCP 8443 |
| Package-store mount | `/srv/jamf-store` |
| Suggested capacity | 1 TB SSD-backed ext4 with 20% free-space protection |
| Jamf secret delivery | Root-owned host environment file passed to the container |
| Certificate | Public certificate obtained through a manual DNS challenge |

Manual DNS validation cannot provide unattended certificate renewal. This configuration is suitable for a controlled pilot only until DNS automation or an internal certificate service is available. Assign an owner and an expiry alert before any managed-client rollout.

The deployment intentionally retains an environment variable for the Jamf client secret because that delivery method was selected. The completed file is mode `0600` outside the Git checkout, but the value remains visible to root and Docker administrators through container inspection. Never paste the value into chat, a terminal command, Compose YAML, Git, tickets, or normal logs.

## Host baseline

Provision an amd64 Ubuntu Server 26.04 LTS host with at least:

- 4 vCPU
- 8 GB RAM
- 1 Gbit/s network connectivity
- A separate 1 TB SSD-backed ext4 filesystem mounted at `/srv/jamf-store`
- A separate system/root volume with at least 40 GB free
- Correct DNS and time synchronization
- Outbound TCP 443 access to the Jamf tenant and the exact approved JCDS hostname
- Inbound TCP 8443 only from `192.168.0.0/16`

Use Docker Engine and the Compose plugin from Docker's official Ubuntu repository. Docker documents Ubuntu 26.04 LTS as supported. Do not use Docker's convenience installation script for production.

Install the host utilities used by this runbook after the base operating system is patched:

```bash
sudo apt update
sudo apt full-upgrade --yes
sudo apt install --yes certbot curl dnsutils git jq openssl
```

Install Docker Engine and `docker-compose-plugin` from Docker's official Ubuntu apt repository, then verify the installed runtime before continuing:

```bash
sudo docker version
sudo docker compose version
```

The package-store filesystem may use `nodev,nosuid,noexec` because the host never executes package contents. The `packages` and `.temporary` directories must be on that same filesystem so publication can use an atomic rename. Do not use `/usr` for package data and do not use an untested NFS mount.

## DNS

Create an `A` record for `jcds-cache.appfruit.ch` that resolves to the host's client-facing private address. Do not publish an internet-routable cache listener merely to obtain a certificate; the manual DNS-01 challenge proves domain control using a TXT record.

Verify from a managed-client network:

```bash
dig +short jcds-cache.appfruit.ch
```

## Package-store and configuration directories

After the dedicated filesystem is mounted, create the runtime directories:

```bash
sudo install -d -m 0755 /srv/jamf-store
sudo install -d -o 65532 -g 65532 -m 0755 /srv/jamf-store/packages
sudo install -d -o 65532 -g 65532 -m 0700 /srv/jamf-store/.temporary
sudo install -d -o root -g root -m 0700 /etc/jcds-content-cache
```

Validate that both data directories use the same filesystem and that capacity is available:

```bash
findmnt /srv/jamf-store
df -h /srv/jamf-store
stat -c '%d %a %u:%g %n' \
  /srv/jamf-store/packages \
  /srv/jamf-store/.temporary
```

The device number printed for both directories must match. The expected ownership is numeric UID/GID `65532:65532`; completed packages become mode `0644`, while temporary files and their directory are private to the helper.

## Source checkout

Until PR #1 is reviewed and merged, deploy the production-candidate branch explicitly:

```bash
sudo git clone \
  --branch codex/m1-streaming-cache \
  --single-branch \
  https://github.com/fabianhartmann2/JCDS-ContentCache.git \
  /opt/jcds-content-cache

cd /opt/jcds-content-cache
```

Record the exact revision used:

```bash
git rev-parse HEAD
```

## Host-local Jamf environment

Install the templates outside the repository:

```bash
sudo install -o root -g root -m 0600 \
  deploy/production/cache-helper.env.example \
  /etc/jcds-content-cache/cache-helper.env

sudo install -o root -g root -m 0644 \
  deploy/production/deployment.env.example \
  /etc/jcds-content-cache/deployment.env

sudoedit /etc/jcds-content-cache/cache-helper.env
sudoedit /etc/jcds-content-cache/deployment.env
```

Replace every `REPLACE` value in `cache-helper.env`:

- `JAMF_TOKEN_URL`: tenant OAuth endpoint ending in `/api/oauth/token`
- `JAMF_CLIENT_ID`: dedicated read-only Jamf API client identifier
- `JAMF_CLIENT_SECRET`: dedicated API client secret; retain single quotes so Compose treats the value literally
- `JAMF_CATALOG_URL`: tenant endpoint ending in `/api/v1/jcds/files`
- `JAMF_RESOLVER_URL_TEMPLATE`: tenant endpoint ending in `/api/v1/jcds/files/{filename}`
- `JCDS_ALLOWED_HOSTS`: the exact hostname from the resolved `uri`, without scheme, path, port, query, wildcard, or IP address

The deprecated Jamf resolver endpoint is intentional until Jamf provides a replacement. Never store a returned signed URL. Multiple approved exact JCDS hostnames may be separated with commas.

Confirm that placeholders are gone without printing the file:

```bash
if sudo grep --quiet REPLACE /etc/jcds-content-cache/cache-helper.env; then
  echo 'Configuration still contains REPLACE values' >&2
  exit 1
fi

sudo stat -c '%a %U:%G %n' \
  /etc/jcds-content-cache/cache-helper.env \
  /etc/jcds-content-cache/deployment.env
```

## Manual DNS certificate issuance

Install Certbot from Ubuntu and request a certificate using a manual DNS challenge:

```bash
sudo apt update
sudo apt install certbot

sudo certbot certonly \
  --manual \
  --preferred-challenges dns \
  --cert-name jcds-cache.appfruit.ch \
  --domain jcds-cache.appfruit.ch \
  --agree-tos \
  --email YOUR-OPERATIONS-EMAIL
```

Certbot will display a `_acme-challenge.jcds-cache.appfruit.ch` TXT record. Create that record in DNS, wait until the TXT value is visible from the authoritative DNS service, and only then continue Certbot.

The required files must then exist:

```text
/etc/letsencrypt/live/jcds-cache.appfruit.ch/fullchain.pem
/etc/letsencrypt/live/jcds-cache.appfruit.ch/privkey.pem
```

Inspect the certificate without exposing the private key:

```bash
sudo openssl x509 \
  -in /etc/letsencrypt/live/jcds-cache.appfruit.ch/fullchain.pem \
  -noout -subject -issuer -dates
```

Because the DNS challenge is manual, normal unattended `certbot renew` is not sufficient. Rerun the same interactive certificate command before expiry, then reload NGINX. The repository's expiry check returns failure when fewer than 30 days remain:

```bash
sudo scripts/check-certificate-expiry.sh
```

After a renewed certificate is present, validate and reload NGINX without interrupting an active package fill:

```bash
sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  exec nginx nginx -t

sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  exec nginx nginx -s reload
```

Run this check daily from the monitoring platform or a systemd timer and alert on a nonzero exit. Certificate automation remains a production-approval gate.

## Validate and start

From `/opt/jcds-content-cache`:

```bash
sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  config --quiet

sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  build --pull

sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  up --detach
```

Check container state and the private readiness path:

```bash
sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  ps

sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  exec nginx wget --quiet --output-document - \
  http://127.0.0.1:8080/health/ready
```

Both long-running containers must report healthy. If they do not, inspect only the recent logs and do not paste unsanitized helper logs into a public channel because helper diagnostics may contain package filenames:

```bash
sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  logs --tail 100 cache-helper nginx
```

## Network controls

NGINX permits the complete `192.168.0.0/16` client range and denies other source addresses. Also enforce the same rule in the host or perimeter firewall. Docker-published ports interact with host firewall rules, so apply restrictions through the approved Docker-aware mechanism, such as the `DOCKER-USER` chain, and validate from both an allowed and denied network before the pilot.

Only TCP 8443 is published. The helper and plaintext health listener remain on the Docker network/container loopback and are not host-published.

## Managed-client validation

From one Mac in `192.168.0.0/16`, verify DNS, certificate trust, and readiness:

```bash
curl --fail --show-error \
  https://jcds-cache.appfruit.ch:8443/health/ready
```

Then request one approved non-sensitive package using its exact flat `.pkg` filename:

```bash
curl --fail --show-error \
  --dump-header /tmp/jcds-cache-first.headers \
  --output /tmp/jcds-cache-test.pkg \
  'https://jcds-cache.appfruit.ch:8443/packages/REPLACE-PACKAGE.pkg'
```

The first request should return `X-Package-Source: JCDS`. Repeat the request; the second response should return `X-Package-Source: LOCAL`. Verify the downloaded package using the normal macOS signature workflow before installation.

After this controlled curl validation, trigger the package through the actual managed-Mac workflow. Curl traffic is classified as `curl`; the real workflow is needed to settle OQ-05 and tune the macOS client classifications.

## Client-behavior records

Extract the privacy-safe NGINX records:

```bash
sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  logs --no-color nginx \
  | sed -n 's/^[^{]*//p' \
  | jq -c 'select(.event == "package_request")'
```

These records omit package paths, package names, query strings, raw ranges, raw user agents, authorization, cookies, and signed URLs. They contain source IP addresses and must remain access-controlled.

## Updates and rollback

Before an update, record the current commit and validate that no fill is active. Pull only reviewed fast-forward changes, rebuild, and recreate the containers without deleting `/srv/jamf-store`:

```bash
cd /opt/jcds-content-cache
git rev-parse HEAD
sudo git pull --ff-only

sudo docker compose \
  --env-file /etc/jcds-content-cache/deployment.env \
  --file deploy/production/compose.yaml \
  up --detach --build
```

For rollback, check out the previously recorded commit, rebuild, and run `up --detach` again. Do not run `down --volumes`, delete `/srv/jamf-store`, or overwrite completed packages during normal rollback.

## Pilot exit conditions

Do not expand beyond the pilot until all of the following are true:

- A real managed Mac successfully completes its normal download/install workflow.
- Actual `GET`, `HEAD`, range, resume, abort, and retry behavior is understood.
- Every legitimate JCDS hostname and redirect is accounted for by exact allowlisting.
- Certificate expiry monitoring has an owner and renewal rehearsal.
- Host firewall enforcement has been validated from allowed and denied networks.
- Disk alerts, log access/retention, backup/rebuild expectations, rollback, and service ownership are approved.
