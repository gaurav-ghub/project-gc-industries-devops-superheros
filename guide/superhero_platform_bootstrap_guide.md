# 🚀 Superhero Platform Bootstrap Guide

This guide bootstraps the entire Superhero Platform locally using:

- Kind
- Istio
- Kyverno
- ArgoCD
- Prometheus
- Grafana
- Loki
- Tempo
- AlertManager
- Slack Alerts
- Kiali
- Distributed Tracing

The setup is intentionally optimized for the `superheros` namespace to keep:

- traces clean
- Kiali graphs focused
- logs relevant
- metrics noise reduced
- debugging easier

---

# 1. CREATE KIND CLUSTER

From project root:

```bash
kind create cluster --name superheros --config kind-config.yaml
```

Verify:

```bash
kubectl get nodes
kubectl cluster-info
```

---

# 2. INSTALL ISTIO

```bash
istioctl install --set profile=demo -y
```

Verify:

```bash
kubectl get pods -n istio-system
istioctl version
```

Enable sidecar injection only for superhero apps:

```bash
kubectl label namespace superheros istio-injection=enabled --overwrite
```

Verify:

```bash
kubectl get ns --show-labels
```

---

# 3. INSTALL KYVERNO

## Add Helm repo

```bash
helm repo add kyverno https://kyverno.github.io/kyverno/
helm repo update
```

## Install Kyverno

```bash
helm install kyverno kyverno/kyverno \
  -n kyverno \
  --create-namespace
```

## Verify

```bash
kubectl get all -n kyverno
```

Wait until all pods become:

```text
1/1 Running
```

---

# 4. INSTALL ARGOCD

## Create namespace

```bash
kubectl create namespace argocd
```

## Install ArgoCD

```bash
kubectl apply -n argocd \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
```

## Verify

```bash
kubectl get pods -n argocd
```

---

# 5. ACCESS ARGOCD

## Port forward

```bash
kubectl port-forward svc/argocd-server -n argocd 8085:443
```

Access:

```text
https://localhost:8085
```

## Get admin password

```bash
kubectl get secret argocd-initial-admin-secret \
  -n argocd \
  -o jsonpath="{.data.password}" | base64 -d
```

---

# 6. CREATE ARGOCD APPLICATIONS

## Superhero Application

```bash
kubectl apply -f infra/argocd/superheros-app.yaml
```

## Kyverno Policies Application

```bash
kubectl apply -f infra/argocd/kyverno-argocd-app.yaml
```

Verify:

```bash
kubectl get applications -n argocd
```

---

# 7. VERIFY APPLICATION DEPLOYMENT

```bash
kubectl get pods -n superheros
kubectl get svc -n superheros
kubectl get virtualservice -n superheros
kubectl get gateway -n superheros
```

Verify Istio sidecars:

```bash
kubectl get pods -n superheros
```

Expected:

```text
2/2 Running
```

---

# 8. ACCESS APPLICATION

## Port forward Istio ingress gateway

```bash
kubectl port-forward -n istio-system svc/istio-ingressgateway 8080:80
```

Access:

```text
http://localhost:8080
```

API Examples:

```text
http://localhost:8080/api/catalog
http://localhost:8080/api/orders
```

---

# 9. INSTALL MONITORING STACK

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
```

```bash
helm install monitoring prometheus-community/kube-prometheus-stack \
  -n monitoring \
  --create-namespace \
  -f values.yaml
```

---

# 10. INSTALL LOKI

```bash
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update
```

```bash
helm install loki grafana/loki-stack \
  -n monitoring \
  -f loki-values.yaml
```

Verify:

```bash
kubectl get pods -n monitoring
```

---

# 11. INSTALL TEMPO

```bash
helm install tempo grafana/tempo \
  -n monitoring
```

Verify:

```bash
kubectl get pods -n monitoring
kubectl get svc -n monitoring
```

---

# 12. CONFIGURE ISTIO TRACING

```bash
kubectl edit configmap istio -n istio-system
```

Inside mesh config add:

```yaml
enableTracing: true

extensionProviders:
- name: tempo
  opentelemetry:
    service: tempo.monitoring.svc.cluster.local
    port: 4317
```

Restart Istiod:

```bash
kubectl rollout restart deployment istiod -n istio-system
```

Restart superhero workloads:

```bash
kubectl rollout restart deployment -n superheros
```

---

# 13. ACCESS GRAFANA

```bash
kubectl port-forward svc/monitoring-grafana -n monitoring 3000:80
```

Access:

```text
http://localhost:3000
```

Default credentials:

```text
admin
prom-operator
```

---

# 14. ENABLE TEMPO SERVICE GRAPH

```bash
kubectl edit configmap tempo -n monitoring
```

Ensure these exist:

```yaml
metrics_generator:
  registry:
    external_labels:
      source: tempo
      cluster: kind

  storage:
    path: /tmp/tempo/generator/wal

  remote_write:
    - url: http://monitoring-kube-prometheus-prometheus.monitoring:9090/api/v1/write

processor:
  service_graphs:
    wait: 10s
    max_items: 10000
    workers: 10

  span_metrics:
    dimensions:
      - service.name
      - operation
      - span.kind
      - status.code
```

Also ensure:

```yaml
overrides:
  defaults:
    metrics_generator:
      processors:
        - service-graphs
        - span-metrics
```

Restart Tempo:

```bash
kubectl delete pod -n monitoring -l app.kubernetes.io/name=tempo --force --grace-period=0
```

---

# 15. GENERATE TRAFFIC

Open repeatedly:

```text
http://localhost:8080
http://localhost:8080/api/catalog
http://localhost:8080/api/orders
```

---

# 16. VIEW DISTRIBUTED TRACING

Grafana → Explore → Tempo

Use:

- Search
- TraceQL
- Service Graph

You can now see:

- request flow
- latency
- service dependencies
- ingress → services
- traces
- spans

---


# 17. ENABLE SLACK ALERTING

## Create Slack Incoming Webhook

Inside Slack:

- Create channel:

```text
#platform-alerts
```

- Open:

```text
Slack Apps → Incoming Webhooks
```

- Enable Incoming Webhooks
- Create webhook for `#platform-alerts`
- Copy webhook URL

---

## Create Alertmanager Secret

IMPORTANT:

The secret MUST exist in SAME namespace as AlertmanagerConfig.

Since AlertmanagerConfig exists in:

```yaml
namespace: superheros
```

the secret must also exist in:

```text
superheros
```

Create secret:

```bash
kubectl create secret generic alertmanager-slack-secret \
-n superheros \
--from-literal=slack_api_url='YOUR_SLACK_WEBHOOK_URL'
```

Verify:

```bash
kubectl get secret -n superheros
```

Expected:

```text
alertmanager-slack-secret
```

---

## Apply Alertmanager Config

```bash
kubectl apply -f infra/monitoring/alertmanager-config.yaml
```

Verify:

```bash
kubectl get alertmanagerconfig -A
```

Expected:

```text
superheros/slack-alerts
```

---

## Restart Alertmanager

```bash
kubectl rollout restart statefulset alertmanager-monitoring-kube-prometheus-alertmanager -n monitoring
```

OR:

```bash
kubectl delete pod -n monitoring -l app.kubernetes.io/name=alertmanager
```

---

## Verify Alertmanager

```bash
kubectl logs -n monitoring alertmanager-monitoring-kube-prometheus-alertmanager-0
```

Verify no Slack-related errors appear.

---

## Access Alertmanager UI

```bash
kubectl port-forward svc/monitoring-kube-prometheus-alertmanager -n monitoring 9093:9093
```

Access:

```text
http://localhost:9093
```

You should see route:

```text
superheros/slack-alerts/slack-notifications
```

---

## Test Slack Alert

Get payment pod:

```bash
kubectl get pods -n superheros
```

Open shell:

```bash
kubectl exec -it <payment-pod> -n superheros -c payment -- sh
```

Kill PID 1:

```bash
kill 1
```

Verify restart:

```bash
kubectl get pods -n superheros
```

Expected:

```text
RESTARTS > 0
```

---

## Verify Alert Fired

Prometheus / Alertmanager should show:

```text
PodRestartingFrequently
```

---

## Expected Slack Notification

```text
🚨 GC Industries Platform Alert

Alert: PodRestartingFrequently

Severity: warning

Namespace: superheros

Pod: payment-xxxxx
```



---

# 18. VERIFY ALERTING

```bash
kubectl logs -n monitoring alertmanager-monitoring-kube-prometheus-alertmanager-0
```

---

# 19. VERIFY LOKI LOGS

Grafana → Explore → Loki

Query:

```logql
{namespace="superheros"}
```

---

# 20. ACCESS KIALI

```bash
kubectl port-forward svc/kiali -n istio-system 20001:20001
```

Access:

```text
http://localhost:20001
```

---

# 21. USEFUL DEBUG COMMANDS

## Pods

```bash
kubectl get pods -A
```

## Events

```bash
kubectl get events -A --sort-by=.metadata.creationTimestamp
```

## Logs

```bash
kubectl logs -f <pod> -n <namespace>
```

## Describe

```bash
kubectl describe pod <pod> -n <namespace>
```

---

# 22. DELETE CLUSTER

```bash
kind delete cluster --name superheros
```

---

# 🚀 FUTURE IMPROVEMENT — FULL PLATFORM BOOTSTRAP AUTOMATION

Right now i am manually installing:

- Istio
- Kyverno
- ArgoCD
- Monitoring stack
- Loki
- Tempo
- Kiali
- AlertManager configs

This is completely normal initially.

But platform engineers eventually automate ALL bootstrap steps.

---

# Best Long-Term Approach

Create:

```text
bootstrap.sh
```

or

```text
Makefile
```

Then run:

```bash
./bootstrap.sh
```

OR:

```bash
make bootstrap
```

That script can automatically:

- create kind cluster
- install Istio
- install Kyverno
- install ArgoCD
- install monitoring stack
- install Loki
- install Tempo
- apply ArgoCD apps
- apply alertmanager configs
- configure namespaces
- restart workloads

Essentially entire platform setup becomes ONE command.

---

# Production Reality

In real companies:

- Terraform creates infra
- Helm installs platform tools
- GitOps deploys applications
- bootstrap pipelines automate everything

Nobody manually runs 40 commands repeatedly in production.