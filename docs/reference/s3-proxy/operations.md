---
type: reference
---

# S3 proxy operations

What the proxy recognises, where the routing decision lives, and
which operations are forwarded vs synthesised vs rejected.

Source:
[`internal/s3proxy/server.go::classifyOperation`](https://github.com/stowage-dev/stowage/blob/main/internal/s3proxy/server.go).

## Classification table

The proxy maps the inbound HTTP method + query string to an operation
name. Unknown shapes get `Unknown` (returns 400).

### `GET`

| Condition | Operation |
|---|---|
| Bucket empty | `ListBuckets` (synthesised) |
| Key empty AND `?location` | `GetBucketLocation` |
| Key empty AND `?uploads` | `ListMultipartUploads` |
| Key empty | `ListObjects` |
| `?uploadId` | `ListParts` |
| else | `GetObject` |

### `PUT`

| Condition | Operation |
|---|---|
| Key empty | `PutBucket` (rejected — admin-only) |
| `?partNumber` AND `?uploadId` | `UploadPart` |
| else | `PutObject` |

### `HEAD`

| Condition | Operation |
|---|---|
| Key empty | `HeadBucket` |
| else | `HeadObject` |

### `DELETE`

| Condition | Operation |
|---|---|
| Key empty | `DeleteBucket` (rejected — admin-only) |
| `?uploadId` | `AbortMultipart` |
| else | `DeleteObject` |

### `POST`

| Condition | Operation |
|---|---|
| `?uploads` | `CreateMultipartUpload` |
| `?uploadId` | `CompleteMultipartUpload` |
| `?delete` | `DeleteObjects` |
| else | `Unknown` |

## What gets forwarded

All recognised operations except `PutBucket` and `DeleteBucket`. Those
return 403 — bucket lifecycle is admin-only and goes via the
dashboard's `/api/backends/{bid}/buckets` handlers, not the proxy.

## ListBuckets synthesis

`ListBuckets` is built from the credential's bucket-scope list. The
upstream is never called. The XML response carries one `<Bucket>`
entry per scope.

## Presigned URLs

Every recognised operation — including the whole multipart lifecycle
(`CreateMultipartUpload`, `UploadPart`, `ListParts`,
`CompleteMultipartUpload`, `AbortMultipart`) — also works with SigV4
query-string signing. There is no server-side presign endpoint: presign
locally against the proxy hostname with your virtual credential, exactly
as with AWS (`aws s3 presign`, every SDK's presigner). Application query
parameters like `uploadId` and `partNumber` are part of the signed query;
the proxy strips only the `X-Amz-*` auth parameters before re-signing
the outbound request. `X-Amz-Expires` is required (1 s – 7 days), as on
AWS.

For browser clients, the bucket's CORS rules must allow the methods used
(`PUT`/`POST`/`DELETE` for multipart) and expose `ETag` so the script
can collect per-part ETags for the complete call.

The `<Location>` element in `CompleteMultipartUploadResult` is rewritten
to the proxy-facing object URL — the upstream endpoint is never exposed.

Flexible-checksum headers (`x-amz-checksum-*`,
`x-amz-sdk-checksum-algorithm`) are forwarded, so SDK default checksums
work across create/part/complete.

## CopyObject

`PUT /<dst-bucket>/<dst-key>` with `x-amz-copy-source` headed at the
source. Stowage forwards it as-is. **The source bucket must also be
in the credential's scope** — otherwise 403.

## Anonymous fast-path

When the request has no `Authorization` header and the target bucket
has an active anonymous binding, the proxy bypasses SigV4 verification
and applies the per-binding allowlist:

- `GET /<bucket>/<key>` → `GetObject`
- `HEAD /<bucket>/<key>` → `HeadObject`
- `GET /<bucket>?...` → `ListObjectsV2`

Everything else returns 401.

## Failure paths

| Phase | Possible result |
|---|---|
| Routing | `400 InvalidRequest` if classification fails. |
| Authentication | `401 InvalidAccessKey`, `401 SignatureDoesNotMatch`. |
| Authorization | `403 AccessDenied` (scope violation), `429 SlowDown` (RPS limit). |
| Quota | `507 EntityTooLarge`. |
| Forward | Whatever the upstream returns. |

## Source files

- Routing: [`router.go`](https://github.com/stowage-dev/stowage/blob/main/internal/s3proxy/router.go).
- Operation classification: [`server.go::classifyOperation`](https://github.com/stowage-dev/stowage/blob/main/internal/s3proxy/server.go).
- Scope check: [`scope.go`](https://github.com/stowage-dev/stowage/blob/main/internal/s3proxy/scope.go).
- Anonymous: [`anonymous.go`](https://github.com/stowage-dev/stowage/blob/main/internal/s3proxy/anonymous.go).
