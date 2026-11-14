<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../../../docs/brand/svg/lockup-horizontal-reversed.svg">
  <img alt="Handbill" src="../../../docs/brand/svg/lockup-horizontal.svg" width="280">
</picture>

# Handbill Helm chart

A Helm chart for **Handbill**, self-hosted private hosting for the web pages your AI agents make (MCP server, web dashboard, and isolated static site file server). For a guided walkthrough, including Docker Compose and plain-container installs, see the [self-hosting guide](../../../docs/self-hosting.md).

## Overview

`handbill` packages a single Go binary and an embedded frontend into one container image:
- **App & MCP**: Serves the web dashboard, authentication, OAuth 2.1 AS, and the Model Context Protocol endpoint (`/mcp`) on `app.origin` (e.g. `https://dash.example.com`).
- **Site File Server**: Serves agent-published static sites on subdomains of `app.sitesDomain` (e.g. `https://<site-id>.sites.example.com`). Each site runs on an isolated origin with strict security boundaries (`nosniff`, `__Host-` cookies, no CORS to site origins).

## Prerequisites

- Kubernetes 1.26+
- Helm 3.10+
- PV provisioner support in the underlying infrastructure (if persistence is enabled)
- A DNS domain with wildcard DNS support (`*.sites.example.com`)

## Quickstart

Install `handbill` with default settings (bundled PostgreSQL StatefulSet and local filesystem storage):

```bash
helm install handbill ./deploy/helm/handbill \
  --namespace handbill \
  --create-namespace \
  --set app.origin="https://dash.example.com" \
  --set app.sitesDomain="sites.example.com"
```

### Installing from the OCI Registry

Published chart versions are hosted in the GitHub Packages OCI registry at `oci://ghcr.io/ferrule-io/charts/handbill`:

```bash
helm install handbill oci://ghcr.io/ferrule-io/charts/handbill \
  --version <VERSION> \
  --namespace handbill \
  --create-namespace \
  --set app.origin="https://dash.example.com" \
  --set app.sitesDomain="sites.example.com"
```

## Domain Architecture and Host Routing

`handbill` multiplexes traffic based on the HTTP `Host` header:
1. `APP_ORIGIN` (e.g. `dash.example.com`): Routes to the management UI, authentication flows, OAuth 2.1 endpoints, and `/mcp`.
2. `*.<SITES_DOMAIN>` (e.g. `<site-id>.sites.example.com`): Routes to the artifact static file server.

On a public instance (anyone outside your organization can publish), put `SITES_DOMAIN` on a separate registrable domain (e.g. `example-sites.com`) or list it in the Public Suffix List. Under the app's own registrable domain, site scripts are same-site with the app and can set cookies on the parent domain: enough to evict a visitor's app session or make app requests too large for the ingress (denial of service; `__Host-` cookies, Origin checks, and CSRF tokens prevent anything worse).

### Ingress Enabled (`ingress.enabled=true`)

When `ingress.enabled=true`, the chart creates a Kubernetes Ingress resource with two rules:
- `dash.example.com` (extracted from `app.origin`)
- `*.sites.example.com` (`*.` prepended to `app.sitesDomain`)

Both rules forward traffic to the `handbill` Service on port 8080.

Example with TLS and cert-manager:

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-dns01
  tls:
    - secretName: handbill-app-tls
      hosts:
        - dash.example.com
    - secretName: handbill-wildcard-tls
      hosts:
        - "*.sites.example.com"
```

**Request body size.** MCP `publish_site` requests can be up to 16 MiB and archive uploads to `/api/deployments/<id>/archive` up to 512 MiB, more than common ingress defaults allow (ingress-nginx: 1 MiB), so raise the limit for the app host, e.g. `nginx.ingress.kubernetes.io/proxy-body-size: 600m` under `ingress.annotations` for ingress-nginx, or a Traefik `buffering` middleware with `maxRequestBodyBytes: 629145600` attached via `traefik.ingress.kubernetes.io/router.middlewares: <namespace>-<middleware>@kubernetescrd`.

### Wildcard DNS and Wildcard TLS Requirements

1. **DNS**:
   - Create an `A` or `CNAME` record for `dash.example.com` pointing to your ingress controller or load balancer.
   - Create a wildcard `A` or `CNAME` record for `*.sites.example.com` pointing to the same ingress controller or load balancer.

2. **TLS (cert-manager & DNS-01)**:
   - Hosted sites are served on arbitrary, randomly generated subdomains (`<site-id>.sites.example.com`).
   - ACME HTTP-01 challenges **cannot** validate wildcard certificates (`*.sites.example.com`).
   - You **must** configure a cert-manager issuer that uses the **DNS-01 challenge** (e.g. Route53, Cloudflare, Google Cloud DNS) to issue the wildcard certificate for `*.sites.example.com`.

### Routing Without the Chart's Ingress (`ingress.enabled=false`)

If you manage routing outside the chart (e.g. using Gateway API, custom Ingress, Traefik, Envoy Gateway, or Cloudflare Tunnels):
1. Keep `ingress.enabled=false` (the default).
2. Ensure your ingress or reverse proxy routes **both** hostnames to the `handbill` Service (`<release-name>-handbill` on port 8080):
   - Exact host: `dash.example.com`
   - Wildcard host: `*.sites.example.com`
3. Ensure the reverse proxy preserves the `Host` header and client IP headers (`X-Forwarded-For`, `X-Forwarded-Proto`).

## Database Configuration

`handbill` requires a PostgreSQL database. The chart supports either a bundled PostgreSQL instance or an external database.

### 1. Bundled PostgreSQL (`postgresql.enabled=true`)

By default, the chart deploys a single-instance PostgreSQL StatefulSet using the official Debian-based `postgres:17` image (runs as UID/GID 999, not Bitnami) with a dedicated PersistentVolumeClaim. If you switch to an Alpine variant (e.g. `postgres:alpine`), update `postgresql.podSecurityContext.fsGroup` and `postgresql.securityContext.runAsUser`/`runAsGroup` to `70` because Postgres runs as UID/GID 70 on Alpine.

- The database password is automatically generated on initial install and preserved across upgrades via Helm `lookup`.
- Alternatively, bring your own password secret via `postgresql.auth.existingSecret` and `postgresql.auth.existingSecretPasswordKey`.

```yaml
postgresql:
  enabled: true
  auth:
    database: handbill
    username: handbill
    existingSecret: "" # optional existing secret
  persistence:
    enabled: true
    size: 10Gi
```

### 2. External PostgreSQL (`postgresql.enabled=false`)

For production workloads, disable bundled PostgreSQL and point to an external PostgreSQL service (e.g. AWS RDS, GCP Cloud SQL, Azure Database, or a self-hosted HA cluster).

#### Option A: Connection parameters with existing Secret for password

```yaml
postgresql:
  enabled: false

externalDatabase:
  host: "postgres.internal.example.com"
  port: 5432
  database: "handbill"
  user: "handbill"
  sslmode: "verify-full"
  existingSecret: "postgres-credentials"
  existingSecretPasswordKey: "password"
```

The container receives standard `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD`, and `PGSSLMODE` environment variables.

`sslmode` defaults to `verify-full`: the connection is encrypted and the server certificate must chain to a trusted root and match `host`. The image trusts the public CA bundle; for a provider or private CA (RDS, Cloud SQL server CAs, a self-signed cluster), mount the CA certificate and point `PGSSLROOTCERT` at it with `extraEnv`. Weaker modes (`require`, `prefer`) encrypt without authenticating the server, so anyone on the network path can impersonate the database. The bundled database (`postgresql.enabled=true`) keeps `sslmode=disable`, since its traffic stays inside the cluster.

#### Option B: Full DSN (`DATABASE_URL`) via existing Secret

```yaml
postgresql:
  enabled: false

externalDatabase:
  existingSecret: "handbill-database-url"
  existingSecretDsnKey: "DATABASE_URL"
```

The container receives `DATABASE_URL` sourced directly from the specified Secret key.

> **Validation Note**: The chart fails rendering if `postgresql.enabled=false` and neither `externalDatabase.host` nor `externalDatabase.existingSecret` (with `existingSecretDsnKey`) is specified.

## Storage Configuration

`handbill` supports three storage backends configured via `storage.type`: `local`, `s3`, or `gcs`.

### 1. Local Filesystem (`storage.type=local`)

Stores site artifacts on a local filesystem directory (default `/data`), backed by a PersistentVolumeClaim.

```yaml
storage:
  type: local
  local:
    dir: "/data"

persistence:
  enabled: true
  accessModes:
    - ReadWriteOnce
  size: 10Gi
```

#### Multi-Replica RWX Caveat

When running multiple replicas (`replicaCount > 1`) with `storage.type=local`:
- All pods must have concurrent read/write access to the same storage volume.
- Therefore, `persistence.accessModes` **must** include `ReadWriteMany` (RWX), provided by a shared storage provider (e.g. NFS, AWS EFS, GCP Filestore, CephFS).
- The chart includes a template validation check: if `replicaCount > 1` and `storage.type=local` without `ReadWriteMany` in `persistence.accessModes`, rendering will fail with an error.
- For multi-replica production setups, using object storage (`s3` or `gcs`) is strongly recommended.

### 2. AWS S3 or S3-Compatible (`storage.type=s3`)

Stores artifacts in an S3 bucket (or S3-compatible systems like MinIO or Cloudflare R2).

```yaml
storage:
  type: s3
  s3:
    bucket: "my-handbill-artifacts"
    region: "us-east-1"
    endpoint: ""       # optional, e.g. "https://s3.us-east-1.amazonaws.com" or MinIO URL
    prefix: "sites"    # optional key prefix
    forcePathStyle: false
    # Optional existing secret for static AWS credentials:
    existingSecret: "aws-s3-credentials"
    accessKeyIdKey: "AWS_ACCESS_KEY_ID"
    secretAccessKeyKey: "AWS_SECRET_ACCESS_KEY"
```

For AWS IRSA (IAM Roles for Service Accounts), leave `existingSecret` empty and annotate the ServiceAccount:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/handbill-s3"
```

### 3. Google Cloud Storage (`storage.type=gcs`)

Stores artifacts in a GCS bucket.

```yaml
storage:
  type: gcs
  gcs:
    bucket: "my-handbill-artifacts"
    prefix: "sites"    # optional prefix
    # Existing Secret containing service-account-key.json
    existingSecret: "gcs-sa-key"
    keyFileKey: "service-account-key.json"
    mountPath: "/etc/gcp"
```

For GKE Workload Identity, leave `existingSecret` empty and annotate the ServiceAccount:

```yaml
serviceAccount:
  annotations:
    iam.gke.io/gcp-service-account: "handbill@project-id.iam.gserviceaccount.com"
```

## Authentication & Security

### Application Secret Key (`SECRET_KEY`)

`handbill` uses `SECRET_KEY` (≥ 32 bytes) for HMAC-SHA256 signing of session tokens, site cookies, and signed upload URLs.
- If `secretKey.existingSecret` is unset, the chart automatically generates a secure 64-character random key and stores it in a Secret. On upgrades, Helm `lookup` preserves the existing key.
- To provide your own pre-created Secret:

```yaml
secretKey:
  existingSecret: "my-app-secret-key"
  existingSecretKey: "secret-key"
```

### Google OIDC Sign-In (Optional)

To enable Google sign-in:

```yaml
google:
  clientId: "1234567890-abcdef.apps.googleusercontent.com"
  existingSecret: "google-oauth-secret"
  existingSecretClientSecretKey: "GOOGLE_CLIENT_SECRET"
```

### SMTP Email Notifications (Optional)

SMTP is used for email verification links and site sharing invite notifications. When `smtp.enabled=false`, all SMTP environment variables are omitted and email notifications are disabled.

```yaml
smtp:
  enabled: true
  host: "smtp.sendgrid.net"
  port: 587
  username: "apikey"
  from: "Handbill <notifications@dash.example.com>"
  tls: "starttls"  # starttls | tls | none
  existingSecret: "smtp-credentials"
  existingSecretPasswordKey: "password"
```

### OAuth 2.1 Dynamic Client Registration

Dynamic Client Registration (RFC 7591) at `/oauth/register` is disabled by default. To enable:

```yaml
oauth:
  dcrEnabled: true
```

## Security Posture & Operational Notes

The chart applies Kubernetes security best practices out-of-the-box:
- **Non-root Execution & Storage Permissions**: Runs as UID/GID `65532` (distroless `nonroot`). When `storage.type=local`, `podSecurityContext.fsGroup: 65532` ensures the persistent volume mounted at `/data` is writable by the container.
- **Privilege Escalation**: Disabled (`allowPrivilegeEscalation: false`).
- **Linux Capabilities**: Dropped (`capabilities.drop: ["ALL"]`).
- **Read-Only Root Filesystem & Temp Directory**: Enabled (`readOnlyRootFilesystem: true`). An ephemeral `emptyDir` is mounted at `/tmp` across all storage modes so site artifact uploads (tar/zip archives) can be unpacked and spooled without root filesystem writes.
- **PostgreSQL Bundled Storage**: The bundled Debian-based PostgreSQL container runs as UID/GID `999` with `fsGroup: 999`. The cluster data directory is set to `PGDATA: /var/lib/postgresql/data/pgdata` (a subdirectory) to prevent `initdb` failures on volumes provisioned with a `lost+found` directory.
- **Startup & Health Probes**: A `startupProbe` on `/healthz` provides up to 150 seconds for PostgreSQL initialization and database migrations to complete before `livenessProbe` and `readinessProbe` start checking the pod.
- **Component Labeling & Port Forwarding**: The application Deployment and Service use `app.kubernetes.io/component: app` to ensure the application Service and port-forward commands target only handbill application pods, not the bundled PostgreSQL StatefulSet pod (`app.kubernetes.io/component: postgresql`).

## Operations

### Limits and rate limits

`limits.*` sets the publishing and request size caps (`MAX_*` variables; see
`docs/design.md#configuration`). Rate limits on sign-up, sign-in, recovery,
OAuth, invites, `/mcp`, archive uploads, and site hosts are on by default
(`rateLimit.enabled`). They are **in memory and per replica**: with N
replicas a client gets up to N times each limit, and a restart resets them.

Behind an ingress or load balancer, set `rateLimit.trustedProxies` to the
CIDRs the proxy connects from (for an in-cluster ingress controller, the pod
CIDR), so handbill takes the client address from `X-Forwarded-For`. Left
empty, every client appears as the proxy and shares one set of limits;
entries from untrusted peers are never believed.

### Metrics

`metrics.enabled=true` serves Prometheus `/metrics` on `metrics.port` (a
separate listener, never the public service) and adds a ClusterIP
`<release>-metrics` Service; `metrics.serviceMonitor.enabled=true` adds a
Prometheus Operator `ServiceMonitor`. Series: `handbill_http_requests_total`
and `handbill_http_request_duration_seconds` (by host class and route
pattern), `handbill_mcp_tool_calls_total`, `handbill_publishes_total`,
`handbill_rate_limited_total`, `handbill_maintenance_*`, `handbill_db_pool_*`,
plus Go runtime and process metrics.

### Graceful shutdown

On SIGTERM the server stops accepting connections and gives in-flight
requests `shutdownTimeout` (25 s) to finish. `preStopSleepSeconds` (5 s)
keeps the pod serving while it is removed from the endpoints first; keep
`terminationGracePeriodSeconds` above the sum of both.

### Maintenance

Every `maintenance.interval` one replica (a PostgreSQL advisory lock picks
it) deletes expired sessions, tickets, sign-in flows, verification links,
and OAuth requests, codes, and tokens; unused OAuth clients; expired or
revoked invites and transfers after 30 days; abandons deployments left open
for 24 h and deletes them after 7 days; retries storage cleanup of deleted
sites; and deletes blobs no version or open deployment uses once they are
older than `maintenance.blobGCGrace` (24 h, at least 1 h).

## Backup and Restore

handbill's state is the PostgreSQL database plus the blob store. The
database says which blobs exist; blobs are immutable and content-addressed
(`sites/<site-id>/blobs/<sha256>`), and nothing but blob GC and site
deletion removes them. Back up and restore so the database never refers to
blobs the storage lacks:

1. **Back up the database first, then the storage.** Blobs written after the
   dump are harmless extras (GC collects them); a database newer than the
   storage copy would point at missing blobs.
2. **Keep blob GC from running against a restored database until the
   storage is restored too**: scale the Deployment to 0 (or set
   `maintenance.interval` very long) during the restore, restore the
   database, restore the storage, then scale back up. GC against an old
   database could otherwise delete blobs that newer copies still need.
3. Take the two copies close together. A site deleted (or a staged blob
   collected) between the database dump and the storage copy leaves the
   dump referring to objects the copy lacks; with bucket versioning those
   objects can still be restored from their previous versions.

**Database.** Bundled PostgreSQL (`postgresql.enabled=true`):

```bash
kubectl exec -n <ns> statefulset/<release>-postgresql -- \
  pg_dump -U handbill -d handbill -Fc > handbill.dump
# restore into an empty database, with handbill scaled to 0:
kubectl exec -i -n <ns> statefulset/<release>-postgresql -- \
  pg_restore -U handbill -d handbill --clean --if-exists --no-owner < handbill.dump
```

External PostgreSQL: use your provider's snapshots or point-in-time recovery,
or `pg_dump -Fc` / `pg_restore` the same way against `externalDatabase.*`.
handbill applies any newer migrations at startup.

**Storage.** S3 or GCS: enable bucket (object) versioning, or replicate the
bucket; restoring means pointing `storage.*` at the copy, or restoring
object versions from before the incident. `local`: snapshot the PVC with a
`VolumeSnapshot` (CSI driver required) or copy `/data` from a pod; restore
by binding the PVC to the snapshot.

## Releasing

Releases are driven entirely by git tags.

### Tagging and Publishing

To release a new version of Handbill:

1. Ensure all changes and CI checks pass on `main`.
2. Tag the release commit following semver (`vX.Y.Z`):
   ```bash
   git tag -a v0.1.0 -m "Release v0.1.0"
   git push origin v0.1.0
   ```
3. The `.github/workflows/release.yaml` workflow triggers on tag push:
   - **Image**: Builds multi-arch container images (`linux/amd64`, `linux/arm64`) from the `Dockerfile` with `VERSION`/`COMMIT` build args (reported by `handbill --version`, the startup log, and the MCP server info), tags them as `X.Y.Z` (the chart's default tag, equal to `appVersion`), `vX.Y.Z`, `X.Y`, and `latest` (for non-prerelease tags), and pushes them to `ghcr.io/ferrule-io/handbill` with SLSA provenance and SBOM attestations.
   - **Chart**: Synchronizes `version` and `appVersion` in `deploy/helm/handbill/Chart.yaml` to the tag version, runs `helm lint`, packages the chart, renders the package with default values and fails unless every rendered `image:` resolves in its registry (`docker buildx imagetools inspect`), then pushes the OCI package to `oci://ghcr.io/ferrule-io/charts`.
   - **GitHub Release**: Drafts and publishes a GitHub release with automatically generated changelog release notes and attaches the packaged `handbill-*.tgz` chart bundle.

### Dry Runs

Dry runs can be triggered manually via `workflow_dispatch` in GitHub Actions by selecting the `dry_run` option. A dry run verifies multi-arch container building and chart linting/packaging without pushing images or charts to registries or creating a GitHub release.

## Configuration Reference

| Parameter | Description | Default |
|---|---|---|
| `replicaCount` | Number of handbill replicas | `1` |
| `image.repository` | Container image repository | `ghcr.io/ferrule-io/handbill` |
| `image.tag` | Image tag (defaults to `.Chart.AppVersion`) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Secrets for private image registries | `[]` |
| `app.origin` | Public URL for application dashboard and MCP endpoint | `https://dash.example.com` |
| `app.sitesDomain` | Base domain under which static sites are served | `sites.example.com` |
| `secretKey.existingSecret` | Existing Secret containing `SECRET_KEY` | `""` |
| `secretKey.existingSecretKey` | Key within `secretKey.existingSecret` | `secret-key` |
| `postgresql.enabled` | Deploy bundled PostgreSQL StatefulSet | `true` |
| `postgresql.image.repository` | Official postgres image repository | `postgres` |
| `postgresql.image.tag` | PostgreSQL image tag | `17` |
| `postgresql.auth.database` | Database name | `handbill` |
| `postgresql.auth.username` | Database user | `handbill` |
| `postgresql.auth.existingSecret` | Secret with existing PostgreSQL password | `""` |
| `postgresql.persistence.enabled`| Enable persistence for PostgreSQL | `true` |
| `postgresql.persistence.size` | Storage size for PostgreSQL PVC | `10Gi` |
| `externalDatabase.host` | External PostgreSQL host | `""` |
| `externalDatabase.port` | External PostgreSQL port | `5432` |
| `externalDatabase.database` | External PostgreSQL database name | `""` |
| `externalDatabase.user` | External PostgreSQL username | `""` |
| `externalDatabase.sslmode` | PostgreSQL SSL mode | `verify-full` |
| `externalDatabase.existingSecret` | Secret containing external DB password or DSN | `""` |
| `externalDatabase.existingSecretPasswordKey` | Key for external DB password | `password` |
| `externalDatabase.existingSecretDsnKey` | Key for full DSN (`DATABASE_URL`) | `""` |
| `storage.type` | Storage adapter: `local`, `s3`, or `gcs` | `local` |
| `storage.local.dir` | Storage directory for local adapter | `/data` |
| `storage.s3.bucket` | S3 bucket name | `""` |
| `storage.s3.region` | S3 AWS region | `""` |
| `storage.s3.endpoint` | S3 endpoint URL (for custom/MinIO storage) | `""` |
| `storage.s3.prefix` | S3 object key prefix | `""` |
| `storage.s3.forcePathStyle` | Enable S3 path-style addressing | `false` |
| `storage.s3.existingSecret` | Secret with AWS credentials | `""` |
| `storage.gcs.bucket` | GCS bucket name | `""` |
| `storage.gcs.prefix` | GCS object key prefix | `""` |
| `storage.gcs.existingSecret` | Secret with GCP service account key | `""` |
| `persistence.enabled` | Enable PVC for `local` storage adapter | `true` |
| `persistence.accessModes` | PVC access modes (`ReadWriteOnce`, `ReadWriteMany`) | `["ReadWriteOnce"]` |
| `persistence.size` | PVC storage size for `local` storage | `10Gi` |
| `ingress.enabled` | Enable Ingress controller resource | `false` |
| `ingress.className` | Ingress class name | `""` |
| `ingress.annotations` | Ingress annotations | `{}` |
| `ingress.tls` | Ingress TLS configurations | `[]` |
| `smtp.enabled` | Enable SMTP email notifications | `false` |
| `smtp.host` | SMTP server host | `""` |
| `smtp.port` | SMTP server port | `587` |
| `smtp.username` | SMTP username | `""` |
| `smtp.from` | SMTP sender email address | `""` |
| `smtp.tls` | SMTP TLS mode (`starttls`, `tls`, `none`) | `starttls` |
| `google.clientId` | Google OAuth Client ID | `""` |
| `google.existingSecret` | Secret with `GOOGLE_CLIENT_SECRET` | `""` |
| `oauth.dcrEnabled` | Enable OAuth Dynamic Client Registration | `false` |
| `inviteTTL` | Duration string for invite and transfer expiration | `336h` |
| `serviceAccount.create` | Create a ServiceAccount | `true` |
| `serviceAccount.annotations` | Annotations for ServiceAccount (e.g. IRSA/Workload Identity) | `{}` |
| `podSecurityContext` | Pod security context (UID/GID 65532) | See `values.yaml` |
| `securityContext` | Container security context (drop ALL, readOnlyRoot) | See `values.yaml` |
| `resources` | Resource requests and limits | `{}` |
| `limits.*` | Publishing and request size caps (`MAX_*`); see `values.yaml` | 512MiB file, 1GiB / 10000 files per version, 512MiB / 10000-entry archives, 16MiB `/mcp`, 1MiB JSON |
| `rateLimit.enabled` | Per-replica in-memory rate limits | `true` |
| `rateLimit.trustedProxies` | CIDRs allowed to set `X-Forwarded-For` | `[]` |
| `metrics.enabled` | Serve `/metrics` on a separate port and Service | `false` |
| `metrics.port` | Metrics listener port | `9090` |
| `metrics.serviceMonitor.enabled` | Create a Prometheus Operator ServiceMonitor | `false` |
| `maintenance.interval` | How often one replica runs cleanup | `15m` |
| `maintenance.blobGCGrace` | Minimum age of an unreferenced blob before deletion | `24h` |
| `shutdownTimeout` | Drain time for in-flight requests on SIGTERM | `25s` |
| `preStopSleepSeconds` | Pause before SIGTERM while endpoints update (0 disables) | `5` |
| `terminationGracePeriodSeconds` | Pod termination grace period | `40` |
