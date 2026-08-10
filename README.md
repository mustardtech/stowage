# stowage

[![Website](https://img.shields.io/badge/website-stowage.dev-0a7cff.svg)](https://stowage.dev)
[![License: AGPL-3.0-or-later](https://img.shields.io/badge/License-AGPL%203.0--or--later-blue.svg)](./LICENSE)
[![CI](https://github.com/stowage-dev/stowage/actions/workflows/ci.yml/badge.svg)](https://github.com/stowage-dev/stowage/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/stowage-dev/stowage?include_prereleases&sort=semver)](https://github.com/stowage-dev/stowage/releases/latest)
[![Go reference](https://pkg.go.dev/badge/github.com/stowage-dev/stowage.svg)](https://pkg.go.dev/github.com/stowage-dev/stowage)


A single Go binary that puts a web dashboard, an AWS-SigV4 S3 proxy, and an optional Kubernetes operator in front of any S3-compatible backend — MinIO, Garage, SeaweedFS, AWS S3, R2, B2, Wasabi. One pane of glass for the storage you already run, with audit, quotas, share links, and per-tenant credentials.

Licensed [AGPL-3.0-or-later](./LICENSE)

## Quickstart

Pick where you want Stowage to live. Full walkthroughs: [one-liner](https://stowage.dev/docs/getting-started/quickstart-oneliner) · [Docker Compose](https://stowage.dev/docs/getting-started/quickstart-compose) · [Kubernetes](https://stowage.dev/docs/getting-started/quickstart-kubernetes).`

### One-liner (Linux, macOS, WSL)

```sh
curl -fsSL https://stowage.dev/install.sh | sh
```

### Windows (PowerShell)

```powershell
irm https://stowage.dev/install.ps1 | iex
```

The installer drops a SHA256-verified binary into the current directory and runs `stowage quickstart`: a managed MinIO, a SQLite DB, a random admin password, and the dashboard at `http://localhost:8080`. Nothing is installed system-wide.

### Docker Compose

```sh
docker compose -f deploy/compose/docker-compose.yml up -d
docker compose -f deploy/compose/docker-compose.yml exec stowage \
  stowage create-admin --username admin --password 'S3cur3-P@ssw0rd!'
```

### Kubernetes (Helm)

```sh
helm install stowage ./deploy/chart \
  --namespace stowage-system --create-namespace \
  --set ingress.enabled=true --set ingress.host=stowage.example.com
kubectl -n stowage-system exec deploy/stowage -- \
  stowage create-admin --username admin --password 'S3cur3-P@ssw0rd!'
```

## What ships in v1.0

Full feature matrix at [stowage.dev/features](https://stowage.dev#features).

**Dashboard.**  local accounts, an object browser with drag-and-drop multipart upload (pause/resume), previews, version history, tags, rename, cross-bucket move/copy, and download-as-zip.

**Public sharing.** Share links with passwords, expiry, race-free download caps, and per-IP rate limiting. No presigned-URL plumbing.

**Per-tenant SDK access.** A second listener (default `:8090`) accepts SigV4 requests with per-tenant virtual credentials, enforces bucket scope, and re-signs to the upstream. Standard AWS SDKs work unmodified — tenants only see the buckets they were granted.

```sh
AWS_ACCESS_KEY_ID=AKIA... AWS_SECRET_ACCESS_KEY=... \
  aws --endpoint-url http://stowage:8090 \
  s3 cp ./hello.txt s3://uploads/hello.txt
```

**Multi-backend workflows.** Cross-backend copy, pinned buckets, and unified search across every configured endpoint.

**Quotas.** Soft and hard caps per bucket — a warning banner at the soft cap, `507 Insufficient Storage` at the hard cap. Applies to dashboard and SDK uploads alike.

**Audit + observability.** SQLite-backed audit log with CSV export, Prometheus `/metrics`, and a [starter Grafana dashboard](./deploy/grafana/stowage.json).

**Kubernetes-native (optional).** A `BucketClaim` CRD provisions a bucket and writes credentials into the requesting namespace; an `S3Backend` CRD declares the upstream. Operator and dashboard ship from the same Helm chart.

## Architecture

One Go binary: an embedded SvelteKit frontend, and pure-Go SQLite for users, sessions, shares, audit events, and credentials. Secrets are sealed with AES-256-GCM under a master key.

Stowage does **not** store object bytes — data lives on the upstream; Stowage proxies access to it.

## Building from source

Requires Go 1.26+ and [Bun](https://bun.sh).

```sh
make frontend    # bun install + bun run build → web/dist/
make build       # bin/stowage
make test
make docker      # multi-stage distroless image
```

Tagged releases publish multi-arch binaries ([downloads](https://stowage.dev/download), [GitHub Releases](https://github.com/stowage-dev/stowage/releases)) and container images on `ghcr.io/stowage-dev/stowage` — cosign-signed, with SBOMs and SLSA provenance. [Verification recipes](https://stowage.dev/docs/security/verify-releases).

## Deploying

Stowage speaks plaintext HTTP and expects TLS from a reverse proxy — see [Reverse proxy](https://stowage.dev/docs/self-host/reverse-proxy) for nginx, Caddy, and Traefik examples, and the [hardening checklist](https://stowage.dev/docs/security/hardening-checklist) before production.

## Security

Report vulnerabilities privately via [GitHub Security Advisories](https://github.com/stowage-dev/stowage/security/advisories/new) — never public issues. Policy and SLAs in [`SECURITY.md`](./SECURITY.md); the [security model](https://stowage.dev/docs/security/model) maps every defence to the source that implements it.

## Contributing

PRs welcome. Sign off your commits (`git commit -s`) under the [DCO](https://developercertificate.org/) — no CLA. See [`CONTRIBUTING.md`](./CONTRIBUTING.md) and [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md).

## License

[AGPL-3.0-or-later](./LICENSE). Run unmodified Stowage anywhere — company, homelab, SaaS — with no publication obligation. Modify it and expose those changes to users over a network, and you must publish them under the same license. ([Why AGPL](https://stowage.dev/docs/explanations/why-agpl))

## Maintainer

Built by [Damian van der Merwe](https://damianvandermerwe.com), an infrastructure & DevOps engineer in Hamilton, New Zealand.
