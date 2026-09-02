# Unattended macOS reboot recovery

This runbook installs and validates the per-user LaunchAgent that restores the
JCDS Content Cache after an approved Mac reboot. It implements the startup
sequence retained in the production-readiness plan.

## Recovery boundary

The mechanism starts only after the dedicated service account has an Aqua GUI
session. FileVault unlock, account login and any approved automatic-login or
MDM session mechanism remain host responsibilities outside this repository.
A root LaunchDaemon is deliberately not used because Docker Desktop requires
the user's GUI bootstrap namespace.

The LaunchAgent:

1. runs at login and every five minutes;
2. starts `/Applications/Docker.app` with `open -gja` when the engine is absent;
3. waits at most five minutes for `docker info` to succeed;
4. validates the protected deployment inputs and their approved SHA-256 values;
5. reconciles the production application with `docker compose up --detach
   --no-build`;
6. waits for `cache-helper`, `cache-maintainer` and `nginx` to become healthy;
7. verifies the trusted HTTPS readiness endpoint without disabling certificate
   validation; and
8. writes credential-free phase timestamps and the latest result under
   `~/JCDS-ContentCache-runtime`.

Failed runs return non-zero and are retried by launchd. Successful runs exit and
are reconciled again on the five-minute interval. Compose's
`restart: unless-stopped` policies continue to cover ordinary container recovery
after the Docker engine is available.

## Prerequisites

Before installation, confirm all of the following on the target Mac:

- Run the commands as the dedicated service account in its GUI session, not as
  `root`.
- Docker Desktop is installed, licensed, initialized and signed in as required.
- Resource Saver is disabled and the approved CPU, memory, disk and update
  settings are applied.
- The reviewed production images have already been built. Reboot recovery never
  builds source or pulls a replacement image.
- The production stack has passed its normal controlled startup and HTTPS smoke
  test.
- These private files and directories exist:

  ```text
  ~/JCDS-ContentCache-runtime/
  ├── cache-helper.production.env
  ├── deployment.production.env
  ├── monitoring/                 # required with --with-monitoring
  └── tls/
      ├── fullchain.pem
      └── privkey.pem
  ```

- `deployment.production.env` uses mode `0600`.
- The dedicated account's approved post-reboot login sequence is configured.

The repository checkout must remain at its installed absolute path. The
controller pins the base and optional monitoring Compose files to the SHA-256
values recorded during installation. A changed deployment file is rejected
until the LaunchAgent is deliberately reinstalled from the reviewed revision.

## Install the LaunchAgent

From the root of the reviewed repository checkout, install the production
configuration currently used with the metrics API and Power Automate webhook:

```bash
./scripts/manage-macos-launchagent.sh install \
  --health-url https://jcds-cache.appfruit.ch:8443/health/ready \
  --with-monitoring
```

Omit `--with-monitoring` only when neither
`deploy/macos-production/compose.monitoring.yaml` consumer is deployed. If the
service certificate is issued by a CA that the dedicated account's `curl` does
not trust through the system trust store, provide the reviewed public CA bundle:

```bash
./scripts/manage-macos-launchagent.sh install \
  --health-url https://jcds-cache.appfruit.ch:8443/health/ready \
  --with-monitoring \
  --ca-file /absolute/path/to/enterprise-ca-chain.pem
```

The installer validates all inputs before unloading an earlier instance. It
then installs:

| Artifact | Installed path | Mode |
|---|---|---|
| LaunchAgent | `~/Library/LaunchAgents/ch.appfruit.jcds-content-cache-recovery.plist` | `0644` |
| Controller | `~/JCDS-ContentCache-runtime/bin/macos-startup-controller.sh` | `0700` |
| Non-secret pinned configuration | `~/JCDS-ContentCache-runtime/startup-controller.conf` | `0600` |
| Operational event log | `~/JCDS-ContentCache-runtime/logs/startup-recovery.jsonl` | `0600` |
| Latest result | `~/JCDS-ContentCache-runtime/startup-recovery.status` | `0600` |

The controller log rotates locally at 5 MiB and retains one previous file. It
contains timestamps, fixed phase names, status and exit code only. It does not
contain credentials, environment values, package names, URLs or dependency
responses.

## Verify before reboot

Inspect the launchd service and latest controller result:

```bash
./scripts/manage-macos-launchagent.sh status
```

Request an immediate idempotent reconciliation:

```bash
./scripts/manage-macos-launchagent.sh reconcile
```

The latest result must have `phase` set to `controller`, `status` set to
`success` and `exit_code` set to `0`. Confirm trusted HTTPS independently:

```bash
curl --fail --show-error --silent \
  https://jcds-cache.appfruit.ch:8443/health/ready
```

Do not use `curl -k` or `--insecure`.

## Cold-boot acceptance test

Installation implements the recovery mechanism but does not close OQ-16 by
itself. Perform one controlled cold-boot test on the target Mac:

1. Record the approved repository commit, installed controller configuration,
   container state and HTTPS readiness before reboot.
2. Reboot the Mac through the approved production procedure without manually
   opening Docker Desktop or running Compose afterward.
3. Confirm the FileVault/login mechanism establishes the dedicated account's
   Aqua session.
4. From a second Mac, poll the trusted readiness endpoint and record the first
   successful timestamp:

   ```bash
   while ! curl --fail --show-error --silent \
     --connect-timeout 5 --max-time 10 \
     https://jcds-cache.appfruit.ch:8443/health/ready >/dev/null; do
     date -u '+%Y-%m-%dT%H:%M:%SZ service unavailable'
     sleep 5
   done
   date -u '+%Y-%m-%dT%H:%M:%SZ service ready'
   ```

5. On the target Mac, collect the credential-free recovery phases:

   ```bash
   tail -n 20 \
     "${HOME}/JCDS-ContentCache-runtime/logs/startup-recovery.jsonl"
   ./scripts/manage-macos-launchagent.sh status
   docker compose \
     --env-file "${HOME}/JCDS-ContentCache-runtime/deployment.production.env" \
     --file deploy/macos-production/compose.yaml \
     --file deploy/macos-production/compose.monitoring.yaml \
     ps --all
   ```

6. Confirm `store-init` exited `0` and `cache-helper`, `cache-maintainer` and
   `nginx` are healthy.
7. Retrieve one previously cached approved package and verify it reports
   `X-Package-Source: LOCAL`.
8. Record the elapsed time from GUI-session availability to trusted remote HTTPS
   readiness and attach the sanitized evidence to OQ-16.

Do not publish the private environment, exact package name, unfiltered helper
logs or webhook URL as acceptance evidence.

## Operations and failure interpretation

The latest `phase` identifies the recovery boundary:

| Phase | Meaning |
|---|---|
| `docker_application` | Docker Desktop is absent or could not be opened. |
| `docker_engine` | Docker Desktop opened, but the engine missed its readiness timeout. |
| `compose_configuration` | The private environment or Compose model is invalid. |
| `compose_reconcile` | Approved images are absent or Compose could not recreate the application. |
| `container_health` | A required container did not become healthy before the timeout. |
| `https_readiness` | Containers are healthy, but the trusted client-facing HTTPS endpoint failed. |
| `lock` | A stale or unsafe controller lock could not be recovered. |

The periodic LaunchAgent retry does not replace external monitoring. The
Power Automate receiver or another external probe must alert when readiness
remains unavailable beyond the approved recovery objective.

## Approved update or path change

After changing the repository revision, Compose files, checkout path, health
URL, monitoring mode or CA bundle, build and validate the approved images first,
then rerun the `install` command. Reinstallation refreshes the deployed
controller and pinned Compose hashes and immediately triggers reconciliation.

## Remove the LaunchAgent

```bash
./scripts/manage-macos-launchagent.sh uninstall
```

Removal unloads the LaunchAgent and deletes only its plist, deployed controller
and non-secret controller configuration. It preserves the production
environment, TLS material, recovery logs, containers and Docker volumes. Stop
or remove the service separately only through the approved production runbook.
