# AI Platform — Alert Enrichment

This module deploys the **Superhero AI Alertmanager**, the enrichment hop that
sits between Prometheus Alertmanager and Slack.

## The path

```
PrometheusRule fires (e.g. a pod restarting in superheros)
  → Alertmanager groups the alerts and routes matching ones to the ai-webhook receiver
  → POST http://superhero-ai-alertmanager.monitoring.svc.cluster.local:8000/alerts
  → the service asks OpenAI (gpt-4o-mini) to explain them like an SRE would
  → the enriched summary is posted to a Slack incoming webhook
```

The Alertmanager route lives in the **monitoring** module's values
(`platform/monitoring/values/base/prometheus-values.yaml`, `alertmanager.config`)
so it is part of the main config and always consumed. The alert itself is defined
by a `PrometheusRule` (`infra/monitoring/pod-restart-alert.yaml`).

## Design

- **The platform owns how alerts reach humans.** Applications only produce
  alerts (metrics + rules); enrichment and delivery are platform concerns, which
  is why this runs in `monitoring` and not in an application namespace.
- **Alertmanager holds no credential.** It only knows this service's in-cluster
  URL. The Slack webhook and the OpenAI key live in this pod's Secret
  (`superhero-ai-secret`), applied from the git-ignored `secret.yaml` — the
  committed `secret.example.yaml` shows the shape. Same rule as Phase 5's Slack
  config: a webhook URL is not a pointer to a credential, it *is* one.
- **Enrichment may never lose an alert.** If OpenAI is unavailable the service
  still delivers a plain, un-enriched alert to Slack; if Slack is down it logs
  and returns 200 so Alertmanager does not retry-storm. Getting the alert to
  Slack is the job; the AI is enhancement on top. This mirrors Phase 5's
  "a notification may never fail a command."

## Files

| File | Purpose |
|---|---|
| `install.sh` / `verify.sh` | module entry + verification, sourced by `bootstrap-kind.sh` |
| `manifests/deployment.yaml` | the enricher Deployment (monitoring ns, platform security posture) |
| `manifests/service.yaml` | the in-cluster address Alertmanager posts to |
| `secret.example.yaml` | credential template; copy to git-ignored `secret.yaml` |
| `versions.yaml` | the pinned image tag; `install.sh` asserts the deployment matches |

## The application

The service's source is in the repo-root `superhero-ai-alertmanager/`
(FastAPI, `app.py`). It is **built in the app repo** and pushed to Docker Hub;
the platform only consumes the image (the CI/CD boundary the platform keeps
everywhere). Rebuild + push a new tag when `app.py` changes, then bump
`versions.yaml` and `manifests/deployment.yaml`.
