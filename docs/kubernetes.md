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

- Kubernetes 1.27 or later, `kubectl`, and Helm 3 or 4 (tested with Helm 4.3).
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
> Choose one way and keep it: see [Admin password](#upgrade) before you change it later.

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
helm upgrade akmatori ./akmatori/charts/akmatori -n akmatori --reset-then-reuse-values -f values-prod.yaml
```

Without `--reset-then-reuse-values` (Helm 3.14 or later), `helm upgrade` drops the values
that you set before with `--set`. Do not use plain `--reuse-values`: it also skips the new
defaults of a newer chart. The simplest rule: keep all your settings in one values file and
always pass it.

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
| `persistence.accessMode` | `ReadWriteOnce` | With `ReadWriteOnce`, the API and agent pods run on one node. |
| `postgresql.enabled` | `true` | `false` = use `externalDatabase.url` or `externalDatabase.existingSecret`. |
| `proxy.httpProxy`, `proxy.httpsProxy` | empty | Outbound HTTP proxy for the API, gateway and agent. |
| `networkPolicy.enabled` | `true` | NetworkPolicies that copy the Docker networks. |
| `networkPolicy.agentEgressDenyCIDRs` | `[]` | CIDRs the agent must not reach (set it for an external database). |
| `*.resources` | small | Requests and limits per component. |

### External PostgreSQL

```bash
kubectl -n akmatori create secret generic akmatori-db \
  --from-literal=database-url='postgres://akmatori:PASSWORD@db.example.com:5432/akmatori?sslmode=require'
helm upgrade --install akmatori ./akmatori/charts/akmatori -n akmatori \
  --set postgresql.enabled=false --set externalDatabase.existingSecret=akmatori-db \
  --set 'networkPolicy.agentEgressDenyCIDRs={10.0.5.20/32}'
```

The chart's NetworkPolicies protect only the bundled PostgreSQL. For an external database,
put its address in `networkPolicy.agentEgressDenyCIDRs` (above, `10.0.5.20/32`; IPv4 and IPv6
are accepted), or let the database accept connections only from the API and gateway pods.

With `agentEgressDenyCIDRs` set, the agent can reach only the API, the gateway, DNS and the
IP ranges outside the list. On Cilium, IP ranges do not match pods: if the agent must reach
something inside the cluster (an HTTP proxy, an on-prem LLM), add a rule for it in
`networkPolicy.agentEgressExtraRules` (see the example in `values.yaml`). NetworkPolicies
only allow traffic, so another policy that allows a listed CIDR for the agent cancels the block.

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

- The agent worker runs as UID 1001 and has no access to the bundled PostgreSQL (NetworkPolicy;
  for an external database see [External PostgreSQL](#external-postgresql)).
  It has no LLM keys or tool secrets in its environment; the API sends LLM keys over the
  WebSocket, and the MCP gateway reads tool credentials from the database.
- The agent mounts skills, context, runbook and memory files read-only. It writes only to
  the per-incident work directories.
- Only the proxy accepts traffic from outside the release. `/mcp/*` (except the read-only
  tool schema list) and `/ws/*` return 404 on the proxy.

## Upgrade

```bash
git -C akmatori pull
helm upgrade akmatori ./akmatori/charts/akmatori -n akmatori --reset-then-reuse-values --wait
```

The Secret `akmatori-secrets` keeps the database password, and the admin password and JWT
secret if you set them, across upgrades, also when a later upgrade does not pass them.
The chart reads the old values from the cluster (Helm `lookup`). `helm template` and client
dry runs cannot do this: they show a new random database password and no admin password.
Do not apply their output to an existing install; use `helm upgrade` (or `--dry-run=server`).

**Admin password.** An `adminPassword` from the chart always wins over the password set in
the setup wizard. To change it, set a new `adminPassword` and upgrade. Akmatori has no
password-change page, and the API does not store an `adminPassword` in the database, so if
you remove it, the API returns to setup mode and the next visitor sets the password. To move
back to a wizard password: disable the Ingress or LoadBalancer first, remove `adminPassword`
from your values, delete the `admin-password` key from the Secret, restart the API, set the
password through `kubectl port-forward`, then enable public access again.

**Database password.** The chart refuses a new `postgresql.password` on an existing install:
PostgreSQL keeps the old password in its data directory. To change it:

1. Run `ALTER ROLE akmatori PASSWORD '<new>'` in PostgreSQL
   (`kubectl -n akmatori exec -it akmatori-postgres-0 -- psql -U akmatori`).
2. Edit the `postgres-password` and `database-url` keys in the Secret `akmatori-secrets`.
   Percent-encode special characters in the URL.
3. Restart the API and the gateway:
   `kubectl -n akmatori rollout restart deploy/akmatori-api deploy/akmatori-mcp-gateway`.
4. If you set `postgresql.password` before, change it to the new password in your values
   file. With `--reset-then-reuse-values`, pass `--set postgresql.password=<new>` once.
   Otherwise the next upgrade compares the old value with the new Secret and stops.

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
| API or agent pod is `Pending` with a pod affinity message | The data volume is `ReadWriteOnce`, so the API and the agent run on one node, and that node is full. Add capacity, or use a `ReadWriteMany` StorageClass with `persistence.accessMode=ReadWriteMany`. |
| Agent init container prints `waiting for /akmatori/...` | The API pod has not started yet. Check `kubectl -n akmatori describe pod -l app.kubernetes.io/component=api`. |
| LLM calls time out | The cluster has no outbound HTTPS. Set `proxy.httpsProxy`, or allow egress to your provider. |
