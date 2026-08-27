# External contract evidence

**Status:** Sanitized OAuth-success, resolver, not-found, metadata, and top-level catalog contracts captured; redirect and remaining error contracts are open  
**Related questions:** OQ-01, OQ-05, OQ-06, OQ-12, and OQ-13

Do not add credentials, access tokens, cookies, signed query parameters, tenant-sensitive package names, package digests, or internal hostnames to this file.

## Jamf OAuth contract

- Token endpoint: Official Jamf documentation specifies `POST /api/v1/oauth/token`; confirm the exact path in the target tenant.
- Request content type: `application/x-www-form-urlencoded`
- Required fields: `client_id`, `client_secret`, and `grant_type=client_credentials`
- Successful response fields: `access_token` (string), `scope` (string), `token_type` (string with observed value `Bearer`), and `expires_in` (number of seconds)
- Expiry behavior: An observed successful response used `expires_in: 59`. The helper clamps its configured early-refresh margin to 20 percent of a shorter observed lifetime so the same valid token can be reused instead of being refreshed for every API call.
- Error status and body shapes:
- Provisional adapter behavior while the exact error bodies remain unknown: classify by HTTP status, discard the bounded response body, and never propagate it or the complete token URL into a client response or normal error log.

### Sanitized successful response

```json
{
  "access_token": "<redacted>",
  "scope": "<api-role-redacted>",
  "token_type": "Bearer",
  "expires_in": 59
}
```

The adapter ignores unknown additional fields, requires a non-empty access token and positive numeric expiry, and accepts the token type case-insensitively only when it is `Bearer`.

## Jamf file-resolution contract

- Selected endpoint and version: Deprecated `GET /api/v1/jcds/files/{fileName}`
- Endpoint decision: Use this endpoint until Jamf introduces a supported replacement. Keep the call behind the adapter and monitor Jamf deprecation notices.
- Required authorization: `Authorization: Bearer <access-token>`
- Required privilege: `Read Jamf Cloud Distribution Service Files`
- Filename encoding behavior: One flat `.pkg` filename was successfully resolved; broader encoding and nested-path behavior remain unverified.
- Download URL JSON field: `uri`
- Not-found status: `404`
- Not-found JSON fields: `httpStatus` with value `404`, and an empty `errors` array
- Unauthorized response:
- Throttle response:
- Server-error response:
- Provisional adapter behavior while these bodies remain unknown: classify by HTTP status, drain and discard the bounded body, retry exactly once only for `401`, and never propagate the body or complete resolver URL into a client response or normal error log.

### Sanitized successful response

```json
{
  "uri": "https://<approved-distribution-host>/<opaque-object-id>?<signed-query-redacted>"
}
```

The resolver response contains only the signed URL. Object integrity metadata is obtained separately from the JCDS file-list endpoint.

### Sanitized not-found response

```json
{
  "httpStatus": 404,
  "errors": []
}
```

## Jamf file-metadata contract

- Observed endpoint: Deprecated `GET /api/v1/jcds/files`
- Endpoint decision: Use this endpoint until Jamf introduces a supported replacement. Keep it behind the same replaceable Jamf adapter boundary.
- Required authorization: `Authorization: Bearer <access-token>`
- Required privilege: `Read Jamf Cloud Distribution Service Files`
- Observed entry fields: `fileName`, `length`, `md5`, `region`, and `sha3`
- Filename match: Exact, case-sensitive match against the requested canonical filename
- Authoritative publication checks: `length` and `sha3`
- Digest interpretation: Observed `sha3` values contain 128 hexadecimal characters and are treated as SHA3-512 digests.
- MD5 policy: Retain for diagnostics/interoperability only; do not use MD5 as the security integrity boundary.
- Top-level shape: The complete observed response begins with `[` and is a JSON array of file entries. No pagination envelope or page metadata is present in the observed contract.
- Defensive behavior: The adapter accepts the observed complete array and fails explicitly if a future response uses an incomplete paginated envelope rather than returning a false not-found result.
- Error behavior: Status-driven categories and the same bounded body/URL redaction rules are implemented; exact unauthorized, throttle, and server-error bodies remain to be captured from the tenant.

### Sanitized metadata fragment

```json
[
  {
    "fileName": "Synthetic_App-1.2.3.pkg",
    "length": 123456789,
    "md5": "<32-hex-characters>",
    "region": "<distribution-region>",
    "sha3": "<128-hex-characters>"
  }
]
```

## Temporary JCDS object contract

- Observed destination class: An HTTPS AWS CloudFront distribution. The exact tenant-specific hostname is intentionally omitted from this public repository and must be supplied through deployment configuration.
- Approved hostname(s): The observed resolver responses use one stable destination hostname, but the production inventory is not yet finalized. Configure exact hostnames; do not use a broad wildcard allowlist.
- Approved redirects: No tenant redirect has yet been observed. Automated tests prove that every redirect is revalidated against the same exact-host policy, that an allowed redirect is followed, and that an unlisted redirect target is rejected before any request reaches it.
- URL lifetime: Encoded in the signed URL's `Expires` query parameter. More samples are required to establish the normal validity duration.
- Signed-query values must never be logged or committed.
- Successful status:
- `Content-Length`:
- `Content-Type`:
- `ETag`:
- `Last-Modified`:
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
- Sanitized by: Codex; all token, tenant, package, object, hostname, digest, and signed-query values removed
- Reviewed by: Pending Jamf and security review
- Decision-register updates: OQ-01, OQ-12 and OQ-13 are resolved for the current deprecated adapter; OQ-06 remains `IN REVIEW`.

## Authoritative references

- [Jamf: Retrieve a list of JCDS files and metadata](https://developer.jamf.com/jamf-pro/reference/get_v1-jcds-files)
- [Jamf: Retrieve a download URL for a specific JCDS file](https://developer.jamf.com/jamf-pro/reference/get_v1-jcds-files-filename)
- [Jamf: Obtain an access token using an API Client](https://developer.jamf.com/jamf-pro/reference/postoauthtoken)
- [Jamf: Privileges and deprecations](https://developer.jamf.com/jamf-pro/docs/privileges-and-deprecations)
