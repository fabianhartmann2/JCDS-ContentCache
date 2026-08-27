# Sanitized live-contract evidence — 2026-08-27

This evidence was captured with `scripts/capture-live-contracts.sh`. Publication of the narrowly scoped sanitized values below was explicitly approved by the project owner.

The published evidence excludes credentials, access tokens, exact hostnames and URLs, package names, package digests, signed values, object paths, API schemas, and HTTP header values.

## Catalog inventory

- Total entries: 194.
- V1 package entries: 191.
- V1 package bytes: 34,587,827,423 bytes (34.59 GB / 32.21 GiB).
- Largest v1 package: 3,276,878,957 bytes (3.28 GB / 3.05 GiB).

## Sanitized destination and HTTP behavior

- Sanitized destination fingerprint: `sha256:307bd94baef9acea`.
- Both HTTP probes used the same fingerprint.
- Redirects observed: zero.

### HEAD probe

- Outcome: completed.
- Status: `200 OK`.
- Content length present: yes.
- Content length matched the aggregate source metadata: yes.
- Content type present: yes.
- Byte-range capability advertised: yes.
- Entity tag present: yes.
- Last-modified metadata present: yes.

### Single-byte range probe

- Request: `Range: bytes=0-0`.
- Outcome: completed.
- Status: `206 Partial Content`.
- Content range present: yes.
- Range honored: yes.
- Content length present: yes.
- Content type present: yes.
- Entity tag present: yes.
- Last-modified metadata present: yes.
