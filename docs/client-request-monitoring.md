# Client request monitoring

## Purpose

NGINX emits one JSON-line behavior record for every request whose URI is in the `/packages/` namespace. The records are designed to answer questions such as:

- Does the managed client use `GET`, `HEAD`, single-range resume, suffix ranges, or multi-range requests?
- Does a request come from the local store or require the JCDS helper?
- Are clients completing transfers, retrying after failures, or repeatedly resuming?
- Which response statuses, byte counts, and latency patterns occur in practice?
- Which client requested which package, and which diagnostic HTTP headers did
  that transfer use?

This is detailed operational request monitoring. A record can identify the
requested package and the network address observed by NGINX.

## Privacy and security boundary

The request log includes the source IP address observed by NGINX, package
filename, raw `User-Agent`, `Range` and `If-Range` request values and raw
`Content-Range` and `Content-Length` response values. Treat all of these as
restricted operational data: limit log access, encrypt transport and storage
in the collector, set an approved retention period, and avoid exporting the
records to systems that do not need client/package-level diagnosis.

The standard NGINX logs still do **not** contain:

- Full URI, path, or query string beyond the validated package filename
- `Authorization`, cookies, referrer, or arbitrary unlisted headers
- Jamf tenant URL, OAuth token, client secret, signed object URL, or signed query
- Request or response bodies

NGINX's conventional per-request error messages can contain the raw request line. The supplied configuration therefore retains only critical NGINX error diagnostics and relies on the structured behavior record plus the helper's sanitized logs for individual request failures.

If requests pass through a load balancer or forward proxy, `$remote_addr` identifies that proxy rather than the Mac. Configure NGINX Real-IP handling only after enumerating trusted proxy addresses; never trust an unrestricted client-supplied `X-Forwarded-For` header. If many Macs share one NAT address, the records can describe aggregate behavior but cannot reliably distinguish individual Macs.

## Record schema

| Field | Type | Meaning |
|---|---|---|
| `event` | string | Always `package_request`. |
| `timestamp` | string | NGINX ISO-8601 completion time. |
| `client` | string | Source IP observed by NGINX. |
| `filename` | string | Validated package filename without its path or query string. |
| `client_kind` | enum | Coarse user-agent classification retained for aggregation. |
| `user_agent` | string | Raw `User-Agent` request header, or empty if absent. |
| `request_id` | string | Per-request 32-character ID, also returned as `X-Request-ID` and sent to the helper. |
| `connection` | integer | NGINX connection identifier. |
| `connection_requests` | integer | Number of requests served on that connection so far. |
| `http_protocol` | string | HTTP protocol reported by NGINX. |
| `method` | string | Request method, normally `GET` or `HEAD`. |
| `range_kind` | enum | Sanitized classification of the request's `Range` header. |
| `range` | string | Raw `Range` request header, or empty if absent. |
| `if_range` | enum | `present` or `absent`. |
| `if_range_value` | string | Raw `If-Range` request header, or empty if absent. |
| `status` | integer | Downstream HTTP response status. |
| `source` | enum/string | `LOCAL`, `JCDS`, `INFLIGHT`, or empty when no package source was selected. `INFLIGHT` means the request shared one active JCDS fill through the growing private temporary file. |
| `response_range` | enum | Whether `Content-Range` was present. |
| `response_content_range` | string | Raw `Content-Range` response header, or empty if absent. |
| `response_length` | enum | Whether `Content-Length` was present. |
| `response_content_length` | string | Raw `Content-Length` response header, or empty if absent. |
| `bytes_sent` | integer | Response-body bytes sent to the client. |
| `request_seconds` | number | Total NGINX request duration in seconds. |
| `upstream_status` | string | Helper response status, or empty for a local response. |
| `upstream_seconds` | string | Helper response time, or empty for a local response. |
| `completion` | enum | `complete` if NGINX completed the request, otherwise `incomplete`. |

Example with documentation-only values:

```json
{"event":"package_request","timestamp":"2026-08-27T12:34:56+00:00","client":"192.0.2.42","filename":"Example.pkg","client_kind":"macos_system","user_agent":"storedownloadd/1.0","request_id":"0123456789abcdef0123456789abcdef","connection":17,"connection_requests":1,"http_protocol":"HTTP/1.1","method":"GET","range_kind":"resume","range":"bytes=1048576-","if_range":"present","if_range_value":"\"example-etag\"","status":206,"source":"LOCAL","response_range":"present","response_content_range":"bytes 1048576-2097151/2097152","response_length":"present","response_content_length":"1048576","bytes_sent":1048576,"request_seconds":0.084,"upstream_status":"","upstream_seconds":"","completion":"complete"}
```

## Request classifications

### Range classes

| Class | Meaning |
|---|---|
| `none` | No `Range` header. |
| `start_zero` | One byte range beginning at byte zero. |
| `resume` | One byte range beginning after byte zero. |
| `suffix` | One suffix byte range. |
| `multi` | The header asks for multiple ranges. |
| `other` | Present but not recognized as one of the forms above. |

Both the classification and raw value are retained. Use the classification for
dashboards and the raw value for individual transfer diagnosis.

### Client classes

| Class | Match intent |
|---|---|
| `jamf` | User agent contains a Jamf identifier. |
| `macos_system` | Common macOS download/update processes such as `storedownloadd`, `softwareupdated`, or `nsurlsessiond`. |
| `macos_installer` | An installer identifier not matched by a more specific class. |
| `curl` | Command-line validation with curl. |
| `browser` | Mozilla-compatible browser traffic. |
| `absent` | No user agent was supplied. |
| `other` | User agent is present but not recognized. |

The rules are deliberately coarse and should be revised from observed
managed-client traffic. The raw `user_agent` remains available for cases where
the coarse class is insufficient.

### Package source classes

| Source | Meaning |
|---|---|
| `JCDS` | This request leads the single upstream transfer and streams it while filling the store. |
| `INFLIGHT` | A concurrent full GET replays the written prefix and follows the same active transfer. |
| `LOCAL` | NGINX or the helper serves a completely published local file, including a range follower released after publication. |

An `INFLIGHT` response is not a second upstream transfer. Like the leading
`JCDS` response, it can begin before final SHA3-512 verification. Only a
`LOCAL` response is known to originate from a completely verified and
atomically published cache file.

## Reading development logs

The Compose service rotates its local Docker JSON logs at 50 MB and retains five files. Extract behavior records with:

```bash
docker compose -f deploy/compose/docker-compose.yml logs --no-color nginx \
  | sed -n 's/^[^{]*//p' \
  | jq -c 'select(.event == "package_request")'
```

Save extracted records temporarily for analysis:

```bash
docker compose -f deploy/compose/docker-compose.yml logs --no-color nginx \
  | sed -n 's/^[^{]*//p' \
  | jq -c 'select(.event == "package_request")' \
  > package-behavior.jsonl
```

Remove the temporary export after the analysis or place it in an approved
restricted log location. It contains package names, raw selected headers and
the network address observed by NGINX. Docker Desktop production traffic may
show its gateway address rather than the original client.

## Reading macOS production logs

Follow detailed package requests on the production Mac:

```bash
runtime_root="${HOME}/JCDS-ContentCache-runtime"

docker compose \
  --env-file "${runtime_root}/deployment.production.env" \
  --file deploy/macos-production/compose.yaml \
  --file deploy/macos-production/compose.monitoring.yaml \
  logs --follow --no-color nginx \
  | sed -n 's/^[^{]*//p' \
  | jq -c 'select(.event == "package_request")'
```

Show the most recent 50 records without following the stream:

```bash
docker compose \
  --env-file "${runtime_root}/deployment.production.env" \
  --file deploy/macos-production/compose.yaml \
  --file deploy/macos-production/compose.monitoring.yaml \
  logs --tail 50 --no-color nginx \
  | sed -n 's/^[^{]*//p' \
  | jq -c 'select(.event == "package_request")'
```

## Useful analyses

Given `package-behavior.jsonl`, summarize behavior combinations:

```bash
jq -s '
  group_by([.client_kind, .method, .range_kind, .status, .source])
  | map({
      client_kind: .[0].client_kind,
      method: .[0].method,
      range_kind: .[0].range_kind,
      status: .[0].status,
      source: .[0].source,
      requests: length
    })
' package-behavior.jsonl
```

Find clients with resume or incomplete activity:

```bash
jq -s '
  group_by(.client)
  | map({
      client: .[0].client,
      requests: length,
      resumes: map(select(.range_kind == "resume")) | length,
      incomplete: map(select(.completion == "incomplete")) | length,
      errors: map(select(.status >= 400)) | length
    })
  | map(select(.resumes > 0 or .incomplete > 0 or .errors > 0))
' package-behavior.jsonl
```

Use `filename`, `client`, `X-Request-ID`, timestamp, status, completion and raw
range values to distinguish retries and resumed transfers. Remember that
Docker Desktop may collapse multiple clients to its gateway address.

## Production integration

- Collect container standard output as JSON lines in the enterprise logging platform.
- Parse only records whose `event` is `package_request`; helper service events use their own sanitized schema.
- Preserve numeric types for status, bytes, duration, and connection counters.
- Build dashboards for method/range/client mix, `LOCAL`/`JCDS`/`INFLIGHT` responses, fan-out followers per fill, response status, incomplete transfers, bytes, and latency percentiles.
- Alert on sustained 5xx rates and abnormal incomplete-transfer rates; tune thresholds from the pilot rather than isolated events.
- Restrict access to client IPs, package names and raw diagnostic headers and
  apply the approved retention policy in the collector.
- Parse only the documented header allowlist. Do not add authorization,
  cookies, referrers, arbitrary request headers, query strings or upstream URLs.

The monitoring-platform choice, alert ownership, retention period, and trusted-proxy configuration remain production decisions under OQ-10 and OQ-02.
