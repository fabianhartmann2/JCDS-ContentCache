# External contract evidence

**Status:** Partial sanitized tenant evidence captured; error and replacement-endpoint contracts remain open  
**Related questions:** OQ-01, OQ-05, OQ-06 and OQ-12

Do not add credentials, access tokens, cookies, signed query parameters, tenant-sensitive package names, or internal hostnames to this file.

## Jamf OAuth contract

- Token endpoint: Official Jamf documentation currently specifies `POST /api/v1/oauth/token`; confirm the exact path in the target tenant.
- Request content type: `application/x-www-form-urlencoded`
- Required fields: `client_id`, `client_secret`, and `grant_type=client_credentials`
- Successful response fields:
- Expiry behavior:
- Error status and body shapes:

## Jamf file-resolution contract

- Observed endpoint and version: `GET /api/v1/jcds/files/{fileName}`
- Required authorization: `Authorization: Bearer <access-token>`
- Required privilege: `Read Jamf Cloud Distribution Service Files`
- Deprecation/replacement status: Jamf's public reference marks the observed endpoint as deprecated; the supported replacement remains to be identified in the tenant's `/api/doc`.
- Filename encoding behavior: One flat `.pkg` filename was successfully resolved; broader encoding and nested-path behavior remain unverified.
- Download URL JSON field: `uri`
- Not-found response:
- Unauthorized response:
- Throttle response:
- Server-error response:

### Sanitized successful response

```json
{
  "uri": "https://<distribution>.cloudfront.net/<opaque-object-id>?<signed-query-redacted>"
}
```

The observed response contains only the signed URL. It does not provide an object size, checksum, ETag, last-modified value or URL lifetime as separate JSON fields.

## Temporary JCDS object contract

- Observed destination class: An HTTPS AWS CloudFront distribution. The exact tenant-specific hostname is intentionally omitted from this public repository and must be supplied through deployment configuration.
- Approved hostname(s): Not finalized. Start with exact observed hostnames; do not use a broad `*.cloudfront.net` allowlist.
- Approved redirects: Not yet observed or tested. Every redirect must be revalidated against the same exact-host policy.
- URL lifetime: Encoded in the signed URL's `Expires` query parameter. More samples are required to establish the normal validity duration.
- Observed signed-query fields: `response-content-disposition`, `url-uuid`, `Expires`, `Signature`, and `Key-Pair-Id`; values must never be logged or committed.
- Successful status:
- `Content-Length`:
- `Content-Type`:
- `ETag`:
- `Last-Modified`:
- Checksum metadata:
- `HEAD` support:
- Single-range support:
- Multi-range support:

## Managed-client request behavior

- Client/process:
- Initial request method:
- `HEAD` behavior:
- `Range` behavior:
- Resume behavior:
- Redirect behavior:
- Relevant request headers:

## Evidence review

- Captured by: Project owner
- Capture date: 2026-08-27
- Sanitized by: Codex; all token, tenant, package, object, hostname and signed-query values removed
- Reviewed by: Pending Jamf and security review
- Decision-register updates: OQ-01 and OQ-06 moved to `IN REVIEW`; OQ-12 remains open because the resolver payload contains no integrity metadata.

## Authoritative references

- [Jamf: Retrieve a download URL for a specific JCDS file](https://developer.jamf.com/jamf-pro/reference/get_v1-jcds-files-filename)
- [Jamf: Obtain an access token using an API Client](https://developer.jamf.com/jamf-pro/reference/postoauthtoken)
