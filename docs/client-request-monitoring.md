# Client request monitoring

## Purpose

NGINX emits one JSON-line behavior record for every request whose URI is in the `/packages/` namespace. The records are designed to answer questions such as:

- Does the managed client use `GET`, `HEAD`, single-range resume, suffix ranges, or multi-range requests?
- Does a request come from the local store or require the JCDS helper?
- Are clients completing transfers, retrying after failures, or repeatedly resuming?
- Which response statuses, byte counts, and latency patterns occur in practice?

This is behavior monitoring, not package inventory logging. A standard record deliberately cannot identify the requested package.

## Privacy and security boundary

The behavior log includes the source IP address because it is the correlation key for a managed client's request sequence. Treat the address as client-identifying operational data: restrict log access, encrypt transport and storage in the collector, set an approved retention period, and avoid exporting it to systems that do not need client-level diagnosis.

The standard NGINX logs do **not** contain:

- URI, path, package name, or query string
- Raw `Range`, `If-Range`, or `User-Agent` values
- `Authorization`, cookies, or referrer
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
| `client_kind` | enum | Coarse user-agent classification; the raw value is discarded. |
| `request_id` | string | Per-request 32-character ID, also returned as `X-Request-ID` and sent to the helper. |
| `connection` | integer | NGINX connection identifier. |
| `connection_requests` | integer | Number of requests served on that connection so far. |
| `http_protocol` | string | HTTP protocol reported by NGINX. |
| `method` | string | Request method, normally `GET` or `HEAD`. |
| `range_kind` | enum | Sanitized classification of the request's `Range` header. |
| `if_range` | enum | `present` or `absent`; the value is not logged. |
| `status` | integer | Downstream HTTP response status. |
| `source` | enum/string | `LOCAL`, `JCDS`, or empty when no package source was selected. |
| `response_range` | enum | Whether `Content-Range` was present; the value is not logged. |
| `response_length` | enum | Whether `Content-Length` was present; the value is not logged. |
| `bytes_sent` | integer | Response-body bytes sent to the client. |
| `request_seconds` | number | Total NGINX request duration in seconds. |
| `upstream_status` | string | Helper response status, or empty for a local response. |
| `upstream_seconds` | string | Helper response time, or empty for a local response. |
| `completion` | enum | `complete` if NGINX completed the request, otherwise `incomplete`. |

Example with documentation-only values:

```json
{"event":"package_request","timestamp":"2026-08-27T12:34:56+00:00","client":"192.0.2.42","client_kind":"macos_system","request_id":"0123456789abcdef0123456789abcdef","connection":17,"connection_requests":1,"http_protocol":"HTTP/1.1","method":"GET","range_kind":"resume","if_range":"present","status":206,"source":"LOCAL","response_range":"present","response_length":"present","bytes_sent":1048576,"request_seconds":0.084,"upstream_status":"","upstream_seconds":"","completion":"complete"}
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

Only the form is retained. Byte offsets and raw header syntax are intentionally discarded.

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

The rules are deliberately coarse and should be revised from observed managed-client traffic. Do not add a classifier that copies arbitrary header text into a record.

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

Remove the temporary export after the analysis or place it in an approved restricted log location. It contains the network address observed by NGINX; Docker Desktop production traffic is expected to show its gateway address rather than the original client.

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

Because package identity is excluded, adjacent requests from one address are only retry candidates, not proof that the same package was retried. Use the returned `X-Request-ID`, timestamp, client address, status, completion, and range class when correlating an approved client-side capture with server-side behavior.

## Production integration

- Collect container standard output as JSON lines in the enterprise logging platform.
- Parse only records whose `event` is `package_request`; helper service events use their own sanitized schema.
- Preserve numeric types for status, bytes, duration, and connection counters.
- Build dashboards for method/range/client mix, local-versus-JCDS responses, response status, incomplete transfers, bytes, and latency percentiles.
- Alert on sustained 5xx rates and abnormal incomplete-transfer rates; tune thresholds from the pilot rather than isolated events.
- Restrict source-IP access and apply the approved retention policy in the collector.
- Keep the default package-identity exclusion. Any temporary diagnostic mode that records package identity requires an explicit security/privacy approval, a short expiry, protected storage, and deletion verification.

The monitoring-platform choice, alert ownership, retention period, and trusted-proxy configuration remain production decisions under OQ-10 and OQ-02.
