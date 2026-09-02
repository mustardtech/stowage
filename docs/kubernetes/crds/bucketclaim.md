---
type: how-to
---

# `BucketClaim`

Namespaced CRD. Declares a logical bucket: the operator creates the
real bucket on the upstream, mints a virtual credential, writes the
consumer Secret, and reports back via status.

## Minimal example

```yaml
apiVersion: broker.stowage.io/v1alpha1
kind: BucketClaim
metadata:
  name: uploads
  namespace: my-app
spec:
  backendRef:
    name: prod-minio
```

The defaults (`Retain` deletion policy, no quota, no anonymous
access, manual rotation, default `bucketNameTemplate`) are sensible
starting points.

## Full example

```yaml
apiVersion: broker.stowage.io/v1alpha1
kind: BucketClaim
metadata:
  name: uploads
  namespace: my-app
spec:
  backendRef:
    name: prod-minio
  bucketName: ""                    # empty = render from template
  deletionPolicy: Retain            # Retain | Delete
  forceDelete: false                # only allowed when deletionPolicy=Delete
  writeConnectionSecretToRef:
    name: uploads-creds
  rotationPolicy:
    mode: TimeBased                 # Manual | TimeBased
    intervalDays: 90                # min 7 when TimeBased
    overlapSeconds: 300
  anonymousAccess:
    mode: ReadOnly                  # None | ReadOnly
    perSourceIPRPS: 0               # 0 = inherit proxy default
  quota:
    soft: 8Gi
    hard: 10Gi
  cors:                             # browser preflight rules (optional)
    - allowedOrigins: ["https://app.example.com"]
      allowedMethods: [GET, PUT, POST]
      maxAgeSeconds: 600
```

## Fields

### `spec.backendRef.name`

Name of the cluster-scoped `S3Backend` to provision against.
Required.

### `spec.bucketName`

Override the bucket name. Empty (default) renders the backend's
`bucketNameTemplate` with `.Namespace`, `.Name`, `.Hash`.

Pattern: `^[a-z0-9][a-z0-9.-]{2,62}$`. **Immutable** once set.

### `spec.deletionPolicy`

| Value | Effect |
|---|---|
| `Retain` (default) | When the claim is deleted, the bucket and its objects stay on the upstream. |
| `Delete` | When the claim is deleted, the operator empties and deletes the bucket. Requires `forceDelete: true` if the bucket isn't already empty. |

### `spec.forceDelete`

`true` allows the operator to delete a non-empty bucket. Admission
webhook rejects `forceDelete: true` unless `deletionPolicy: Delete`.

### `spec.writeConnectionSecretToRef.name`

Name of the consumer Secret to write in the claim's namespace. The
Secret carries the credentials and connection metadata your Pods
consume:

```
AWS_ACCESS_KEY_ID
AWS_SECRET_ACCESS_KEY
AWS_REGION
AWS_ENDPOINT_URL
AWS_ENDPOINT_URL_S3
BUCKET_NAME
S3_ADDRESSING_STYLE
```

Mount it with `envFrom: secretRef:` or via `valueFrom.secretKeyRef:`
on individual env vars.

### `spec.rotationPolicy`

| Field | Default | Notes |
|---|---|---|
| `mode` | `Manual` | `Manual` or `TimeBased`. |
| `intervalDays` | 90 | Minimum 7 when `TimeBased`. |
| `overlapSeconds` | 300 | Time both old and new credentials are accepted during rotation. |

See [Credential rotation](../rotation.md) for the full mechanics.

### `spec.anonymousAccess`

Opt the bucket into anonymous read access through the proxy.

| Field | Default | Notes |
|---|---|---|
| `mode` | `None` | `None` or `ReadOnly`. |
| `perSourceIPRPS` | 0 | Per-client-IP RPS cap. 0 means inherit `s3_proxy.anonymous_rps` (default 20). |

See [Anonymous read buckets](../anonymous.md).

### `spec.quota`

Optional per-bucket storage limits enforced by the proxy.

| Field | Notes |
|---|---|
| `soft` | Kubernetes Quantity. Warning threshold; uploads still allowed. |
| `hard` | Kubernetes Quantity. Cap; uploads past this rejected with HTTP 507 (`EntityTooLarge`). |

When both are set, soft must be ≤ hard. The operator copies these
values into the consumer Secret as decimal-byte fields
(`quota_soft_bytes`, `quota_hard_bytes`) so the proxy's
`KubernetesLimitSource` reads them off the same informer.

### `spec.cors`

Optional browser cross-origin rules for the bucket, answered by the
proxy at preflight time (the backend bucket is never consulted).

| Field | Notes |
|---|---|
| `allowedOrigins` | Required. Exact-match against the `Origin` header; `"*"` allows any. |
| `allowedMethods` | Required. Any of `GET`, `HEAD`, `PUT`, `POST`, `DELETE`. |
| `allowedHeaders` | Optional. Empty means the proxy's permissive SigV4 + POST-Object default set. |
| `exposeHeaders` | Optional. Empty means the proxy default (`ETag` and friends). |
| `maxAgeSeconds` | Optional. 0 means the proxy default (600). |

The operator writes the rules to a `bucket-cors` Secret (data field
`cors_rules`) the proxy's Kubernetes informer watches. Rules configured
in the dashboard (Bucket settings → CORS) for the same bucket are
**unioned** with these — a preflight succeeds if any rule from either
source covers the origin and method. Removing `spec.cors` deletes the
Secret and closes the bucket to browser preflights again.

## Status

| Field | Notes |
|---|---|
| `status.phase` | `Pending` → `Creating` → `Bound` (or `Failed`); `Deleting` during deletion. |
| `status.bucketName` | The real bucket name (post-template rendering). |
| `status.proxyEndpoint` | URL tenants should point SDKs at. |
| `status.boundSecretName` | The consumer Secret the claim wrote. |
| `status.accessKeyId` | Latest access key ID. |
| `status.rotatedAt` | Timestamp of last rotation. |
| `status.observedGeneration` | Last `metadata.generation` reconciled. |
| `status.conditions[type=Ready]` | `True` when phase is `Bound`. |

## Finalizer

The operator owns the finalizer
`broker.stowage.io/bucketclaim-protection`. It runs the
deletionPolicy on `kubectl delete` and removes the finalizer when
the upstream side is consistent. To force-remove a stuck claim, take
care: removing the finalizer with the upstream still holding the
bucket will orphan the bucket on the upstream.

## kubectl printer columns

```sh
kubectl -n my-app get bucketclaims
# NAME      BUCKET           PHASE   READY   AGE
# uploads   my-app-uploads   Bound   True    3h
```

## Source

- Type: [`internal/operator/api/v1alpha1/bucketclaim_types.go`](https://github.com/stowage-dev/stowage/blob/main/internal/operator/api/v1alpha1/bucketclaim_types.go)
- CRD manifest: [`deploy/chart/crds/broker.stowage.io_bucketclaims.yaml`](https://github.com/stowage-dev/stowage/blob/main/deploy/chart/crds/broker.stowage.io_bucketclaims.yaml)
- Reconciler: [`internal/operator/controller/`](https://github.com/stowage-dev/stowage/tree/main/internal/operator/controller)
