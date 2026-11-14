<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/svg/lockup-horizontal-reversed.svg">
    <img alt="Handbill" src="brand/svg/lockup-horizontal.svg" width="300">
  </picture>
</p>

# Self-hosting Handbill

Handbill is one container image (`ghcr.io/ferrule-io/handbill`) plus
PostgreSQL. It holds every site your agents publish on infrastructure you
control. This guide takes you from nothing to a production instance.

- [What you need](#what-you-need)
- [Choose your domains](#choose-your-domains)
- [Option A: Docker Compose on one server](#option-a-docker-compose-on-one-server)
- [Option B: Kubernetes with Helm](#option-b-kubernetes-with-helm)
- [Option C: Just the container](#option-c-just-the-container)
- [Sign-in and email](#sign-in-and-email)
- [Connecting agents](#connecting-agents)
- [Upgrades, backups, and monitoring](#upgrades-backups-and-monitoring)
- [Troubleshooting](#troubleshooting)

## What you need

| | Notes |
|---|---|
| **Two hostnames** | One for the app (`handbill.example.com`) and a wildcard for sites (`*.example-sites.com`). |
| **Wildcard DNS** | `A`/`CNAME` records for both, pointing at your server or load balancer. |
| **Wildcard TLS certificate** | For `*.example-sites.com`. ACME issues wildcards only through the **DNS-01** challenge, so your reverse proxy or cert-manager needs API access to your DNS provider. |
| **PostgreSQL 17+** | Bundled by the Compose file and the Helm chart, or bring your own (RDS, Cloud SQL, …). |
| **Storage for site files** | A local volume, an S3 bucket (or S3-compatible: MinIO, Cloudflare R2, …), or a GCS bucket. |
| *Optional:* SMTP | For emailed invites and email verification. Everything works without it; invites are always available as links. |
| *Optional:* Google OAuth client | Adds "Continue with Google" next to passkeys. |

Handbill reads all of its configuration from environment variables. Invalid
configuration stops startup with one message listing every problem. The full
reference is in [design.md → Configuration](design.md#configuration). The
variables you will set most often:

| Variable | Example | Purpose |
|---|---|---|
| `APP_ORIGIN` | `https://handbill.example.com` | Where the app, sign-in, and `/mcp` live |
| `SITES_DOMAIN` | `example-sites.com` | Parent domain of the site hosts |
| `SECRET_KEY` | `openssl rand -hex 32` | Signs site cookies and upload URLs; at least 32 bytes |
| `DATABASE_URL` | `postgres://…` | Or the libpq `PG*` variables |
| `STORAGE_TYPE` | `local`, `s3`, `gcs` | Plus `STORAGE_LOCAL_DIR`, `S3_*`, or `GCS_*` |
| `TRUSTED_PROXIES` | `10.0.0.0/8` | Proxies allowed to set `X-Forwarded-For` (for rate limits) |

## Choose your domains

Every site is served from its own random subdomain of `SITES_DOMAIN`, for
example `k3vq7m2xa4pd.example-sites.com`. That isolation is what keeps one
agent's HTML and JavaScript away from your session and from other sites.

**If people outside your organization can publish, put `SITES_DOMAIN` on a
separate registrable domain** (`example-sites.com`, not
`sites.example.com`). Under the app's own domain, site scripts are "same-site"
with the app and can flood the parent domain's cookie jar, signing visitors
out of the app. Handbill's `__Host-` cookies, Origin checks, and CSRF tokens
keep this to a nuisance rather than a breach, but a separate domain avoids it
entirely. For a private team instance, `sites.example.com` is fine.

## Option A: Docker Compose on one server

The [`deploy/compose`](../deploy/compose) directory runs Handbill and
PostgreSQL, with site files on a Docker volume. A small VM is enough.

**1. Configure.**

```bash
git clone https://github.com/ferrule-io/handbill.git
cd handbill/deploy/compose
cp .env.example .env
```

Edit `.env`:

```dotenv
SECRET_KEY=<openssl rand -hex 32>
POSTGRES_PASSWORD=<openssl rand -hex 16>
HANDBILL_VERSION=0.1.2            # pin a release
APP_ORIGIN=https://handbill.example.com
SITES_DOMAIN=example-sites.com
HANDBILL_BIND=127.0.0.1:8080      # only the reverse proxy talks to it
TRUSTED_PROXIES=172.16.0.0/12     # Docker's bridge networks
```

**2. Start it.**

```bash
docker compose up -d
docker compose logs -f handbill   # look for "listening"
```

**3. Put a reverse proxy in front.** The proxy terminates TLS for both
hostnames and forwards everything to `127.0.0.1:8080`, keeping the original
`Host` header.

<details open>
<summary><b>Caddy</b> (automatic wildcard certificates)</summary>

Caddy needs a [DNS provider module](https://caddyserver.com/docs/automatic-https#dns-challenge)
for the wildcard certificate. Build it with `xcaddy build --with
github.com/caddy-dns/cloudflare` (or your provider), or use a prebuilt image
that includes it.

```caddyfile
handbill.example.com, *.example-sites.com {
	tls {
		dns cloudflare {env.CLOUDFLARE_API_TOKEN}
	}
	reverse_proxy 127.0.0.1:8080
}
```

Caddy keeps the `Host` header, sets `X-Forwarded-For`, and imposes no
request body limit by default.

</details>

<details>
<summary><b>nginx</b></summary>

Obtain the wildcard certificate with your ACME client's DNS-01 plugin (for
example certbot with `certbot-dns-cloudflare`), then:

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name handbill.example.com *.example-sites.com;

    ssl_certificate     /etc/letsencrypt/live/handbill/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/handbill/privkey.pem;

    client_max_body_size    600m;   # large uploads (default MAX_FILE_SIZE is 512 MiB)
    proxy_request_buffering off;
    proxy_buffering         off;    # stream video and large files
    proxy_read_timeout      30m;
    proxy_send_timeout      30m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

</details>

**4. Open `https://handbill.example.com`** and create the first account.
Anyone who can reach the instance can sign up, so firewall it to your
network or VPN if it should stay internal.

## Option B: Kubernetes with Helm

The chart is published as an OCI artifact. It bundles an optional
PostgreSQL StatefulSet and an optional Ingress, generates `SECRET_KEY` on
first install, and runs the pod non-root with a read-only root filesystem.

**Kick the tires** with the bundled database and a local volume:

```bash
helm install handbill oci://ghcr.io/ferrule-io/charts/handbill \
  --namespace handbill --create-namespace \
  --set app.origin=https://handbill.example.com \
  --set app.sitesDomain=example-sites.com
```

**For production**, use an external database, object storage, and an Ingress
with a wildcard certificate. A `values.yaml` for EKS with S3 through IRSA and
ingress-nginx with cert-manager might look like this:

```yaml
replicaCount: 2

app:
  origin: https://handbill.example.com
  sitesDomain: example-sites.com

postgresql:
  enabled: false
externalDatabase:
  host: handbill.cluster-abc123.us-east-1.rds.amazonaws.com
  database: handbill
  user: handbill
  existingSecret: handbill-db          # key: password
  # sslmode defaults to verify-full; for a private CA see the chart README

storage:
  type: s3
  s3:
    bucket: acme-handbill-sites
    region: us-east-1
persistence:
  enabled: false

serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/handbill-s3

ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-dns01
    nginx.ingress.kubernetes.io/proxy-body-size: 600m
    nginx.ingress.kubernetes.io/proxy-read-timeout: "1800"
  tls:
    - secretName: handbill-app-tls
      hosts: [handbill.example.com]
    - secretName: handbill-sites-wildcard-tls
      hosts: ["*.example-sites.com"]

rateLimit:
  trustedProxies: ["10.0.0.0/8"]       # your ingress controller's pod CIDR

metrics:
  enabled: true
  serviceMonitor:
    enabled: true
```

```bash
helm install handbill oci://ghcr.io/ferrule-io/charts/handbill \
  --version 0.1.2 --namespace handbill --create-namespace -f values.yaml
```

Things to know:

- **More than one replica** needs S3/GCS, or a `ReadWriteMany` volume for the
  `local` adapter; the chart refuses to render otherwise.
- **Rate limits are per replica and in memory.** With N replicas a client
  gets up to N times each limit.
- **Raise your ingress's body size limit.** MCP requests can be up to 16 MiB
  and file uploads up to `limits.maxFileSize` (512 MiB by default);
  ingress-nginx defaults to 1 MiB.

Every value, plus GCS, Workload Identity, SMTP, and backup/restore, is in the
[chart README](../deploy/helm/handbill/README.md).

## Option C: Just the container

On Cloud Run, ECS, Fly.io, Nomad, or plain `docker run`, set the environment
and route both hostnames to port 8080:

```bash
docker run -d --name handbill -p 8080:8080 \
  -e APP_ORIGIN=https://handbill.example.com \
  -e SITES_DOMAIN=example-sites.com \
  -e SECRET_KEY="$(openssl rand -hex 32)" \
  -e DATABASE_URL='postgres://handbill:…@db.internal:5432/handbill?sslmode=verify-full' \
  -e STORAGE_TYPE=s3 -e S3_BUCKET=acme-handbill-sites -e S3_REGION=us-east-1 \
  ghcr.io/ferrule-io/handbill:0.1.2
```

The runtime contract:

- Listens on `:8080` and runs as UID/GID 65532. The `local` adapter writes
  under `/data`; mount a volume there.
- Applies database migrations at startup (safe with several replicas starting
  at once) and exits non-zero if the database is unreachable.
- `GET /healthz` is liveness; `GET /readyz` checks the database and storage.
  Both answer on every host except site hosts.
- Routes by `Host`: `APP_ORIGIN`'s host serves the app; `<id>.SITES_DOMAIN`
  serves sites; anything else gets 404.
- Logs JSON lines to stdout. Query strings and auth path segments are never
  logged.
- On SIGTERM, drains in-flight requests for `SHUTDOWN_TIMEOUT` (25 s).
- S3 credentials come from the AWS default chain (environment, IRSA, instance
  role); GCS from Application Default Credentials.
- Keep `SECRET_KEY` stable: rotating it signs everyone out of sites and
  invalidates every recovery code.

## Sign-in and email

**Passkeys** work out of the box. Browsers allow them only on `https://`
origins and on `localhost`, so production needs TLS. The WebAuthn relying
party is `APP_ORIGIN`'s host; changing that host later invalidates existing
passkeys (users can still get in with recovery codes or Google).

**Google sign-in**: create an OAuth client (type *Web application*) in Google
Cloud, add the authorized redirect URI
`https://handbill.example.com/auth/google/callback`, and set
`GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`.

**Email**: set `SMTP_HOST`, `SMTP_FROM` (e.g. `Handbill <handbill@example.com>`),
and optionally `SMTP_PORT`, `SMTP_USERNAME`/`SMTP_PASSWORD`, and `SMTP_TLS`
(`starttls`, `tls`, or `none`). With SMTP, Handbill emails invites and verifies
addresses. Without it, invites are links you send yourself.

## Connecting agents

Point any MCP client at `https://handbill.example.com/mcp`. The first
connection opens a consent screen in the browser; approved clients are listed,
and can be disconnected, under **Settings → Security**.

- **Claude Code**: `claude mcp add --transport http handbill https://handbill.example.com/mcp`
- **Claude Desktop / claude.ai**: Settings → Connectors → *Add custom
  connector*. These connect from Anthropic's cloud, so the instance must be
  reachable from the public internet.
- **VS Code**: *MCP: Add Server…* → HTTP.
- **Clients without Client ID Metadata Document support** need Dynamic Client
  Registration: set `OAUTH_DCR_ENABLED=true` (Helm: `oauth.dcrEnabled`).

## Upgrades, backups, and monitoring

**Upgrading**: pin a version (`HANDBILL_VERSION`, Helm `--version`), read the
[release notes](https://github.com/ferrule-io/handbill/releases), bump it,
and restart. Migrations run automatically at startup.

**Backups**: Handbill's state is the PostgreSQL database plus the blob store.
Site files are immutable and content-addressed, so:

1. Back up **the database first**, then the storage (volume snapshot, bucket
   versioning, or replication).
2. When restoring, restore both before Handbill runs maintenance against the
   restored database. Otherwise blob garbage collection can delete files the
   storage copy still needs.

The [chart README](../deploy/helm/handbill/README.md#backup-and-restore) has
the step-by-step commands.

**Monitoring**: set `METRICS_ADDR=:9090` (Helm: `metrics.enabled`) to expose
Prometheus metrics on a separate listener: HTTP request counts and latency by
host class and route, MCP tool calls, publishes, rate-limit hits, maintenance
runs, and database pool and Go runtime stats.

**Housekeeping** runs on its own every `MAINTENANCE_INTERVAL` (15 min). It
expires sessions and tokens, abandons uploads left open for 24 h,
garbage-collects files no version uses, and finishes deleting removed sites.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Every site URL returns 404 | The proxy isn't forwarding `*.SITES_DOMAIN`, or it rewrites `Host`. Handbill routes on the original `Host` header. |
| Startup fails with `invalid configuration` | The message lists each bad variable. A common one: `APP_ORIGIN`'s host must not be inside `SITES_DOMAIN`. |
| Large uploads fail with 413 | Your proxy's body limit (nginx's default is 1 MiB) is below Handbill's limit. Raise it. |
| Passkey prompt never appears | The app isn't served over HTTPS (or `localhost`), or the browser's URL doesn't match `APP_ORIGIN`. |
| All users seem to share one rate limit | Set `TRUSTED_PROXIES` to your proxy's address so Handbill reads `X-Forwarded-For`. |
| Videos won't seek | A proxy is stripping `Range` requests or buffering responses. Handbill answers ranges with `206`. |
| Claude.ai can't connect | The instance isn't reachable from the internet, or TLS isn't valid for `APP_ORIGIN`. |
| `/readyz` returns 503 | The database or storage didn't answer within 3 s. The response body names which one. |
