# Access — the platform's front door

The module that makes everything else reachable. Before it existed, every
dashboard on this platform was behind a `kubectl port-forward`: one terminal per
URL, a process that dies with the shell that started it, and four commands to
remember before anyone could look at anything.

It runs **last** in the bootstrap chain. Every route it applies points at a
component an earlier module installed.

## What it installs

| Thing | Why it is here |
|---|---|
| **Kiali** (`kiali-server`, pinned in `versions.yaml`) | the mesh console — the only place a canary split is a picture rather than a percentage in a YAML file. The one component this module owns. |
| **`Gateway/endurance-gateway`** (`manifests/gateway.yaml`) | the platform's single ingress listener. One, because two Gateways claiming port 80 on the same proxy is a conflict, not two front doors. |
| **`VirtualService/endurance-dashboards`** (`manifests/dashboards.yaml`) | the platform's own routes on it. |

## How an address reaches a pod

```
browser  http://localhost:8080/grafana
   |
   |  kind-config.yaml: host 8080 -> node 30000
   v
kind node container
   |
   |  platform/networking/istio/kind-gateway.yaml: nodePort 30000 -> gateway :80
   v
istio-ingressgateway
   |
   |  Gateway/endurance-gateway  +  VirtualService/endurance-dashboards
   v
prometheus-grafana.monitoring:80
```

Two numbers hold that together — `30000` and `8080` — and they live in two
files. `kind-config.yaml` publishes the node port to the host; the Istio overlay
pins the Service to that node port. A Go test reads both files and fails if they
stop agreeing, because a mismatch produces a cluster that installs perfectly and
answers nothing.

## No URI rewriting

Each component is configured to *serve* from its own subpath rather than being
rewritten into one:

| Path | Component | What makes it work |
|---|---|---|
| `/argocd` | argocd-server | `server.rootpath` + `server.basehref` + `server.insecure` in `platform/gitops/argocd/values.yaml` |
| `/kiali` | kiali | `server.web_root` in `kiali/values.yaml` |
| `/grafana` | prometheus-grafana | `grafana.ini` `root_url` + `serve_from_sub_path` in the monitoring values |
| `/prometheus` | prometheus | `routePrefix` + `externalUrl` in the monitoring values |
| `/alertmanager` | alertmanager | `routePrefix` + `externalUrl` in the monitoring values |

A UI served at `/` and reached at `/grafana` builds absolute links to
`/public/...` and breaks on its first stylesheet. Rewriting the URI at the
gateway fixes the first request and nothing after it. So the component that
renders a link is the component that is told its own address.

## Applications

The platform does **not** route applications. An application declares a route in
`specs/<app>.yaml`:

```yaml
route:
  enabled: true
  path: /
  service: frontend
```

`endurance onboard` renders that into `apps/<app>/values.yaml`, `charts/app`
turns it into a VirtualService bound to `istio-system/endurance-gateway`, and
ArgoCD applies it. Nothing in this directory knows an application's URL
structure — which is the property that lets the platform serve an application it
has never seen.

## Running it on its own

```bash
bash platform/access/install.sh      # needs Istio, and a published gateway
bash platform/access/uninstall.sh    # routes and Kiali; leaves Istio alone
```

`endurance urls` is the check: it prints every address and reports which of them
answered.
