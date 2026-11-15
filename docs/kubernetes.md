# Install Akmatori on Kubernetes

The Helm chart in [`charts/akmatori`](../charts/akmatori) installs the same services as
`docker-compose.yml`, from the same GHCR images:

| Component | Kind | Notes |
|---|---|---|
| `akmatori-api` | Deployment (1 replica) | REST API, webhooks, crons, agent WebSocket |
| `akmatori-agent` | Deployment (1 replica) | pi agent worker; no database access |
| `akmatori-mcp-gateway` | Deployment | Tool calls with run-scoped auth |
| `akmatori-frontend` | Deployment | Web UI (static files) |
| `akmatori` | Deployment + Service | nginx proxy, the only entry point |
| `akmatori-postgres` | StatefulSet | PostgreSQL 16; optional, you can use an external database |

The names above are for the release name `akmatori`. Another release name gives `<release>-akmatori-*`.

## Requirements

- Kubernetes 1.27 or later, `kubectl`, and Helm 3.
- A default StorageClass (or set `persistence.storageClass` and `postgresql.persistence.storageClass`).
- About 0.5 vCPU and 1.5 GiB of memory for requests; the agent uses more while it investigates.
- Outbound HTTPS from the cluster to your LLM provider and to `ghcr.io`.
- For the network isolation: a CNI that enforces NetworkPolicy (Calico, Cilium, and others).
  On other CNIs the policies have no effect, and the install still works.

## Install

```bash
git clone --depth 1 https://github.com/akmatori/akmatori.git
helm upgrade --install akmatori ./akmatori/charts/akmatori \
  --namespace akmatori --create-namespace --wait
kubectl -n akmatori get pods
```

All six pods must be `Running` and `READY 1/1`. The first start takes 1–3 minutes: the
API waits for PostgreSQL and runs the database migrations.

### Open the UI and set the admin password

The proxy Service is `ClusterIP` by default. Use a port-forward for the first login:

```bash
kubectl -n akmatori port-forward svc/akmatori 8080:80
```

Open <http://localhost:8080/>. The setup wizard asks for the admin password (user `admin`).
Until you set it, the API is in setup mode: the agent worker log shows
`Unexpected server response: 503` and retries. This is normal. After the setup,
the log shows `Connected to ws://akmatori-api:3000/ws/agent`.

> **Set the admin password before you expose the UI.** In setup mode, the first visitor
> sets the password. To skip the wizard, set `adminPassword` (or `existingAuthSecret`).

Then do the same steps as for Docker: **Settings → LLM Provider**, connectors, alert sources.

## Expose Akmatori

Send all traffic to the `akmatori` Service. The proxy keeps the MCP tool endpoints and the
agent WebSocket internal, so do not route paths to the other Services.

**Ingress** (example with ingress-nginx and cert-manager):

```yaml
# values-prod.yaml
baseUrl: https://akmatori.example.com
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
    nginx.ingress.kubernetes.io/proxy-body-size: 50m
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
  hosts:
    - host: akmatori.example.com
  tls:
    - secretName: akmatori-tls
      hosts: [akmatori.example.com]
```

```bash
helm upgrade --install akmatori ./akmatori/charts/akmatori -n akmatori -f values-prod.yaml
```

**LoadBalancer** (no Ingress controller): set `proxyServer.service.type=LoadBalancer`.
Use `proxyServer.service.loadBalancerSourceRanges` to limit the source addresses. This gives
plain HTTP; put TLS in front of it (a CDN, or a load balancer with a certificate).

Alert webhooks use the same address: `https://<host>/webhook/alert/<instance-uuid>`.

## Configuration

The main values (see [`values.yaml`](../charts/akmatori/values.yaml) for all of them):

| Value | Default | Description |
|---|---|---|
| `image.tag` | chart `appVersion` | Image tag for the four Akmatori images. Use `main` to track the main branch. |
| `baseUrl` | empty | Public URL for links in Slack and other channels. |
| `adminPassword`, `jwtSecret` | empty | Skip the setup wizard / fix the JWT secret. |
| `existingAuthSecret` | empty | Secret with the keys `admin-password` and `jwt-secret`. |
| `persistence.size` | `10Gi` | Shared data volume (`/akmatori`). |
| `persistence.accessMode` | `ReadWriteOnce` | With `ReadWriteOnce`, the agent pod runs on the API pod's node. |
| `postgresql.enabled` | `true` | `false` = use `externalDatabase.url` or `externalDatabase.existingSecret`. |
| `proxy.httpProxy`, `proxy.httpsProxy` | empty | Outbound HTTP proxy for the API, gateway and agent. |
| `networkPolicy.enabled` | `true` | NetworkPolicies that copy the Docker networks. |
| `*.resources` | small | Requests and limits per component. |

### External PostgreSQL

```bash
kubectl -n akmatori create secret generic akmatori-db \
  --from-literal=database-url='postgres://akmatori:PASSWORD@db.example.com:5432/akmatori?sslmode=require'
helm upgrade --install akmatori ./akmatori/charts/akmatori -n akmatori \
  --set postgresql.enabled=false --set externalDatabase.existingSecret=akmatori-db
```

### Corporate HTTP proxy

```bash
helm upgrade --install akmatori ./akmatori/charts/akmatori -n akmatori \
  --set proxy.httpProxy=http://proxy.corp:3128 --set proxy.httpsProxy=http://proxy.corp:3128
```

The chart adds the in-cluster Service names, `.svc` and `.cluster.local` to `NO_PROXY`.
Image pulls through a proxy are a node (container runtime) setting; allow `ghcr.io` and
`pkg-containers.githubusercontent.com`.

## Security model

The chart keeps the isolation of the Docker install:

- The agent worker runs as UID 1001 and has no access to PostgreSQL (NetworkPolicy).
  It has no LLM keys or tool secrets in its environment; the API sends LLM keys over the
  WebSocket, and the MCP gateway reads tool credentials from the database.
- The agent mounts skills, context, runbook and memory files read-only. It writes only to
  the per-incident work directories.
- Only the proxy accepts traffic from outside the release. `/mcp/*` (except the read-only
  tool schema list) and `/ws/*` return 404 on the proxy.

## Upgrade

```bash
git -C akmatori pull
helm upgrade akmatori ./akmatori/charts/akmatori -n akmatori --reuse-values --wait
```

The generated database password stays in the Secret `akmatori-secrets` across upgrades.
To pin a build, set `image.tag` (for example `0.4.0` or `main-<sha>`). The API and the agent
use the `Recreate` strategy, so there is a short downtime during an upgrade.

Back up the database before an upgrade:

```bash
kubectl -n akmatori exec akmatori-postgres-0 -- pg_dump -U akmatori akmatori | gzip > akmatori-$(date +%F).sql.gz
```

## Uninstall

```bash
helm uninstall akmatori -n akmatori
```

Helm keeps the data volume `akmatori-data`, the Secret `akmatori-secrets`, and the PostgreSQL
volume `data-akmatori-postgres-0`, so a new install finds the old data. To remove all data:

```bash
kubectl delete namespace akmatori
```

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| Agent log: `Unexpected server response: 503` | Setup mode. Set the admin password in the UI. |
| `akmatori-postgres-0` is `Pending` | No default StorageClass. Set `postgresql.persistence.storageClass`. |
| Agent pod is `Pending` with a pod affinity message | The data volume is `ReadWriteOnce` and the API pod's node is full. Add capacity, or use a `ReadWriteMany` StorageClass with `persistence.accessMode=ReadWriteMany`. |
| Agent init container prints `waiting for /akmatori/...` | The API pod has not started yet. Check `kubectl -n akmatori describe pod -l app.kubernetes.io/component=api`. |
| LLM calls time out | The cluster has no outbound HTTPS. Set `proxy.httpsProxy`, or allow egress to your provider. |
