#!/usr/bin/env bash
set -euo pipefail

# scripts/kind-smoke.sh: Verifies the handbill Helm chart in a local kind cluster.
# Covers:
#   (a) Defaults (bundled PostgreSQL, local storage PVC)
#   (b) External database (plain postgres:17 + PG* env vars)
#   (c) External DSN secret variant (existingSecretDsnKey)
#   (d) Ingress enabled (ingress-nginx on kind with --resolve)
#   (e) Helm upgrade idempotency & data persistence

CLUSTER_NAME="${KIND_CLUSTER_NAME:-handbill-smoke}"
IMAGE_NAME="${HANDBILL_IMAGE:-handbill:smoke}"
CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../deploy/helm/handbill" && pwd)"
APP_HOST="dash.example.com"
SITES_DOMAIN="sites.example.com"
SAMPLE_SITE_ID="abcdef234567"
SITE_HOST="${SAMPLE_SITE_ID}.${SITES_DOMAIN}"

# Use mise exec if mise is present, else system tools directly
if command -v mise >/dev/null 2>&1; then
  KUBECTL="mise exec -- kubectl"
  HELM="mise exec -- helm"
  KIND="mise exec -- kind"
else
  KUBECTL="kubectl"
  HELM="helm"
  KIND="kind"
fi

pass_count=0
fail_count=0

log() {
  echo -e "\n=== $* ==="
}

pass() {
  echo -e "  [PASS] $*"
  pass_count=$((pass_count + 1))
}

fail() {
  echo -e "  [FAIL] $*" >&2
  fail_count=$((fail_count + 1))
}

cleanup_namespace() {
  local ns="$1"
  if $KUBECTL get ns "$ns" >/dev/null 2>&1; then
    echo "Cleaning up namespace $ns..."
    $HELM uninstall --namespace "$ns" "$($HELM list -n "$ns" -q)" >/dev/null 2>&1 || true
    $KUBECTL delete ns "$ns" --timeout=60s >/dev/null 2>&1 || true
  fi
}

# Ensure kind cluster exists with extraPortMappings for ingress
ensure_cluster() {
  if ! $KIND get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    log "Creating kind cluster ${CLUSTER_NAME} with ingress port mappings..."
    cat <<EOF | $KIND create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 80
    hostPort: 80
    protocol: TCP
  - containerPort: 443
    hostPort: 443
    protocol: TCP
EOF
  fi

  # Ensure image is present in kind cluster
  log "Loading ${IMAGE_NAME} into kind cluster..."
  $KIND load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"
}

# Verify endpoints over port-forward
verify_port_forward() {
  local ns="$1"
  local svc_name="$2"
  local local_port="${3:-18080}"
  local pf_pid=""

  # Start port-forward
  $KUBECTL -n "$ns" port-forward "svc/${svc_name}" "${local_port}:8080" >/dev/null 2>&1 &
  pf_pid=$!

  # Wait for port-forward to connect
  local tries=0
  while ! curl -s "http://127.0.0.1:${local_port}/healthz" >/dev/null 2>&1; do
    tries=$((tries + 1))
    if [ "$tries" -gt 30 ]; then
      kill "$pf_pid" 2>/dev/null || true
      fail "port-forward to svc/${svc_name} timed out"
      return 1
    fi
    sleep 0.5
  done

  # 1. /healthz with Host: dash.example.com
  local hz_body hz_code
  hz_body=$(curl -s "http://127.0.0.1:${local_port}/healthz" -H "Host: ${APP_HOST}")
  hz_code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${local_port}/healthz" -H "Host: ${APP_HOST}")
  if [ "$hz_code" = "200" ] && [ "$hz_body" = "ok" ]; then
    pass "${ns}: /healthz returned 200 ok with Host: ${APP_HOST}"
  else
    fail "${ns}: /healthz expected 200 ok, got ${hz_code}: ${hz_body}"
  fi

  # 2. /readyz with Host: dash.example.com
  local rz_body rz_code
  rz_body=$(curl -s "http://127.0.0.1:${local_port}/readyz" -H "Host: ${APP_HOST}")
  rz_code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${local_port}/readyz" -H "Host: ${APP_HOST}")
  if [ "$rz_code" = "200" ] && [ "$rz_body" = "ok" ]; then
    pass "${ns}: /readyz returned 200 ok with Host: ${APP_HOST}"
  else
    fail "${ns}: /readyz expected 200 ok, got ${rz_code}: ${rz_body}"
  fi

  # 3. Host: <12 a-z2-7 chars>.sites.example.com should return site server's 302/404, not the UI
  local site_code
  site_code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${local_port}/" -H "Host: ${SITE_HOST}")
  if [ "$site_code" = "302" ] || [ "$site_code" = "404" ]; then
    pass "${ns}: Host: ${SITE_HOST} returned site server response (${site_code}), not app UI"
  else
    fail "${ns}: Host: ${SITE_HOST} expected 302 or 404, got ${site_code}"
  fi

  kill "$pf_pid" 2>/dev/null || true
  wait "$pf_pid" 2>/dev/null || true
}

ensure_cluster

# ---------------------------------------------------------------------------
# Case (a) & (e): Defaults and Helm upgrade persistence
# ---------------------------------------------------------------------------
log "Running Case (a): Default bundled PostgreSQL & local storage PVC..."
NS_DEFAULT="smoke-defaults"
cleanup_namespace "$NS_DEFAULT"

$HELM install smoke-def "$CHART_DIR" \
  --namespace "$NS_DEFAULT" \
  --create-namespace \
  --set image.repository="${IMAGE_NAME%:*}" \
  --set image.tag="${IMAGE_NAME##*:}" \
  --set image.pullPolicy=Never \
  --set app.origin="https://${APP_HOST}" \
  --set app.sitesDomain="${SITES_DOMAIN}"

log "Waiting for rollout in ${NS_DEFAULT}..."
$KUBECTL -n "$NS_DEFAULT" rollout status statefulset smoke-def-handbill-postgresql --timeout=120s
$KUBECTL -n "$NS_DEFAULT" rollout status deployment smoke-def-handbill --timeout=120s

verify_port_forward "$NS_DEFAULT" "smoke-def-handbill" 18080

# Case (e): helm upgrade with no changes
log "Running Case (e): Helm upgrade idempotency & data persistence check..."
SK_BEFORE=$($KUBECTL -n "$NS_DEFAULT" get secret smoke-def-handbill-secret-key -o jsonpath='{.data.secret-key}')
PG_BEFORE=$($KUBECTL -n "$NS_DEFAULT" get secret smoke-def-handbill-postgresql -o jsonpath='{.data.password}')

$HELM upgrade smoke-def "$CHART_DIR" \
  --namespace "$NS_DEFAULT" \
  --set image.repository="${IMAGE_NAME%:*}" \
  --set image.tag="${IMAGE_NAME##*:}" \
  --set image.pullPolicy=Never \
  --set app.origin="https://${APP_HOST}" \
  --set app.sitesDomain="${SITES_DOMAIN}"

SK_AFTER=$($KUBECTL -n "$NS_DEFAULT" get secret smoke-def-handbill-secret-key -o jsonpath='{.data.secret-key}')
PG_AFTER=$($KUBECTL -n "$NS_DEFAULT" get secret smoke-def-handbill-postgresql -o jsonpath='{.data.password}')

if [ "$SK_BEFORE" = "$SK_AFTER" ] && [ -n "$SK_BEFORE" ]; then
  pass "Helm upgrade: SECRET_KEY preserved identically across upgrade"
else
  fail "Helm upgrade: SECRET_KEY changed or empty (before: $SK_BEFORE, after: $SK_AFTER)"
fi

if [ "$PG_BEFORE" = "$PG_AFTER" ] && [ -n "$PG_BEFORE" ]; then
  pass "Helm upgrade: PostgreSQL password preserved identically across upgrade"
else
  fail "Helm upgrade: PostgreSQL password changed or empty"
fi

log "Waiting for deployment rollout post-upgrade..."
$KUBECTL -n "$NS_DEFAULT" rollout status deployment smoke-def-handbill --timeout=120s
verify_port_forward "$NS_DEFAULT" "smoke-def-handbill" 18080

# Verify goose_db_version migration table still exists
GOOSE_COUNT=$($KUBECTL -n "$NS_DEFAULT" exec smoke-def-handbill-postgresql-0 -- \
  psql -U handbill -d handbill -t -A -c "SELECT count(*) FROM goose_db_version;" || echo "0")
if [ "$GOOSE_COUNT" -gt 0 ]; then
  pass "Helm upgrade: goose_db_version exists and has applied migrations (count: ${GOOSE_COUNT})"
else
  fail "Helm upgrade: goose_db_version table missing or empty"
fi

cleanup_namespace "$NS_DEFAULT"

# ---------------------------------------------------------------------------
# Case (b): External database with PG* variables
# ---------------------------------------------------------------------------
log "Running Case (b): External PostgreSQL with PG* vars..."
NS_EXT="smoke-ext-db"
cleanup_namespace "$NS_EXT"
$KUBECTL create ns "$NS_EXT"

# Deploy standalone postgres:17
cat <<EOF | $KUBECTL apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ext-postgres
  namespace: ${NS_EXT}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ext-postgres
  template:
    metadata:
      labels:
        app: ext-postgres
    spec:
      containers:
      - name: postgres
        image: postgres:17
        env:
        - name: POSTGRES_PASSWORD
          value: extsecret99
        - name: POSTGRES_USER
          value: extuser
        - name: POSTGRES_DB
          value: extdb
        ports:
        - containerPort: 5432
---
apiVersion: v1
kind: Service
metadata:
  name: ext-postgres
  namespace: ${NS_EXT}
spec:
  ports:
  - port: 5432
  selector:
    app: ext-postgres
---
apiVersion: v1
kind: Secret
metadata:
  name: ext-db-secret
  namespace: ${NS_EXT}
type: Opaque
stringData:
  db-password: extsecret99
EOF

log "Waiting for external postgres in ${NS_EXT}..."
$KUBECTL -n "$NS_EXT" rollout status deployment ext-postgres --timeout=120s

$HELM install smoke-ext "$CHART_DIR" \
  --namespace "$NS_EXT" \
  --set image.repository="${IMAGE_NAME%:*}" \
  --set image.tag="${IMAGE_NAME##*:}" \
  --set image.pullPolicy=Never \
  --set app.origin="https://${APP_HOST}" \
  --set app.sitesDomain="${SITES_DOMAIN}" \
  --set postgresql.enabled=false \
  --set externalDatabase.host=ext-postgres \
  --set externalDatabase.port=5432 \
  --set externalDatabase.database=extdb \
  --set externalDatabase.user=extuser \
  --set externalDatabase.sslmode=disable \
  --set externalDatabase.existingSecret=ext-db-secret \
  --set externalDatabase.existingSecretPasswordKey=db-password

log "Waiting for handbill in ${NS_EXT}..."
$KUBECTL -n "$NS_EXT" rollout status deployment smoke-ext-handbill --timeout=120s
verify_port_forward "$NS_EXT" "smoke-ext-handbill" 18081
cleanup_namespace "$NS_EXT"

# ---------------------------------------------------------------------------
# Case (c): External database with full DSN secret
# ---------------------------------------------------------------------------
log "Running Case (c): External database with full DSN secret..."
NS_DSN="smoke-ext-dsn"
cleanup_namespace "$NS_DSN"
$KUBECTL create ns "$NS_DSN"

cat <<EOF | $KUBECTL apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dsn-postgres
  namespace: ${NS_DSN}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: dsn-postgres
  template:
    metadata:
      labels:
        app: dsn-postgres
    spec:
      containers:
      - name: postgres
        image: postgres:17
        env:
        - name: POSTGRES_PASSWORD
          value: dsnsecret88
        - name: POSTGRES_USER
          value: dsnuser
        - name: POSTGRES_DB
          value: dsndb
        ports:
        - containerPort: 5432
---
apiVersion: v1
kind: Service
metadata:
  name: dsn-postgres
  namespace: ${NS_DSN}
spec:
  ports:
  - port: 5432
  selector:
    app: dsn-postgres
---
apiVersion: v1
kind: Secret
metadata:
  name: dsn-secret
  namespace: ${NS_DSN}
type: Opaque
stringData:
  dsn-key: postgres://dsnuser:dsnsecret88@dsn-postgres:5432/dsndb?sslmode=disable
EOF

log "Waiting for postgres in ${NS_DSN}..."
$KUBECTL -n "$NS_DSN" rollout status deployment dsn-postgres --timeout=120s

$HELM install smoke-dsn "$CHART_DIR" \
  --namespace "$NS_DSN" \
  --set image.repository="${IMAGE_NAME%:*}" \
  --set image.tag="${IMAGE_NAME##*:}" \
  --set image.pullPolicy=Never \
  --set app.origin="https://${APP_HOST}" \
  --set app.sitesDomain="${SITES_DOMAIN}" \
  --set postgresql.enabled=false \
  --set externalDatabase.existingSecret=dsn-secret \
  --set externalDatabase.existingSecretDsnKey=dsn-key

log "Waiting for handbill in ${NS_DSN}..."
$KUBECTL -n "$NS_DSN" rollout status deployment smoke-dsn-handbill --timeout=120s
verify_port_forward "$NS_DSN" "smoke-dsn-handbill" 18082
cleanup_namespace "$NS_DSN"

# ---------------------------------------------------------------------------
# Case (d): Ingress enabled (ingress-nginx on kind)
# ---------------------------------------------------------------------------
log "Running Case (d): Ingress enabled..."
NS_ING="smoke-ingress"
cleanup_namespace "$NS_ING"

# Ensure ingress-nginx is installed
if ! $KUBECTL get ns ingress-nginx >/dev/null 2>&1; then
  log "Installing ingress-nginx on kind..."
  $KUBECTL apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml
fi
log "Waiting for ingress-nginx controller readiness..."
$KUBECTL wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=120s

$HELM install smoke-ing "$CHART_DIR" \
  --namespace "$NS_ING" \
  --create-namespace \
  --set image.repository="${IMAGE_NAME%:*}" \
  --set image.tag="${IMAGE_NAME##*:}" \
  --set image.pullPolicy=Never \
  --set app.origin="http://${APP_HOST}" \
  --set app.sitesDomain="${SITES_DOMAIN}" \
  --set ingress.enabled=true \
  --set ingress.className=nginx

log "Waiting for rollout in ${NS_ING}..."
$KUBECTL -n "$NS_ING" rollout status statefulset smoke-ing-handbill-postgresql --timeout=120s
$KUBECTL -n "$NS_ING" rollout status deployment smoke-ing-handbill --timeout=120s

# Wait for ingress controller to sync rules
sleep 5

# Check curl through ingress-nginx using --resolve
# 1. /healthz on app host
ing_hz=$(curl -s --resolve "${APP_HOST}:80:127.0.0.1" "http://${APP_HOST}/healthz")
if [ "$ing_hz" = "ok" ]; then
  pass "${NS_ING}: Ingress curl /healthz returned 'ok' for ${APP_HOST}"
else
  fail "${NS_ING}: Ingress curl /healthz failed for ${APP_HOST}: got '$ing_hz'"
fi

# 2. /readyz on app host
ing_rz=$(curl -s --resolve "${APP_HOST}:80:127.0.0.1" "http://${APP_HOST}/readyz")
if [ "$ing_rz" = "ok" ]; then
  pass "${NS_ING}: Ingress curl /readyz returned 'ok' for ${APP_HOST}"
else
  fail "${NS_ING}: Ingress curl /readyz failed for ${APP_HOST}: got '$ing_rz'"
fi

# 3. App UI root
ing_root_code=$(curl -s -o /dev/null -w "%{http_code}" --resolve "${APP_HOST}:80:127.0.0.1" "http://${APP_HOST}/")
if [ "$ing_root_code" = "200" ]; then
  pass "${NS_ING}: Ingress curl / returned 200 for ${APP_HOST}"
else
  fail "${NS_ING}: Ingress curl / expected 200 for ${APP_HOST}, got ${ing_root_code}"
fi

# 4. Site host root (wildcard routing)
ing_site_code=$(curl -s -o /dev/null -w "%{http_code}" --resolve "${SITE_HOST}:80:127.0.0.1" "http://${SITE_HOST}/")
if [ "$ing_site_code" = "302" ] || [ "$ing_site_code" = "404" ]; then
  pass "${NS_ING}: Ingress curl wildcard route returned ${ing_site_code} for ${SITE_HOST}"
else
  fail "${NS_ING}: Ingress curl wildcard route expected 302/404 for ${SITE_HOST}, got ${ing_site_code}"
fi

cleanup_namespace "$NS_ING"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
log "Kind Smoke Test Results"
echo "Passed: $pass_count"
echo "Failed: $fail_count"

if [ "$fail_count" -gt 0 ]; then
  echo "SMOKE TESTS FAILED" >&2
  exit 1
fi

echo "ALL SMOKE TESTS PASSED"
exit 0
