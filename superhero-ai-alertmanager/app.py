###############################################################################
# File: app.py
#
# Description:
# Superhero AI Alertmanager — the enrichment hop between Prometheus Alertmanager
# and Slack for the GC Industries Cloud Native Platform.
#
# The flow:
#   Prometheus (PrometheusRule) fires ->
#   Alertmanager groups + routes matching alerts to this service's /alerts ->
#   this service asks OpenAI to explain them like an SRE would ->
#   the enriched summary is posted to a Slack incoming webhook.
#
# Design rules (mirroring the platform's Phase 5 notification decisions):
#
#   1. Alertmanager holds NO Slack or model credential. It only knows the URL of
#      this in-cluster service. The credentials live here, in this pod's Secret.
#
#   2. Enrichment may never lose an alert. Once the request body parses, /alerts
#      returns 200 no matter what. If OpenAI is down we still deliver a plain,
#      un-enriched alert to Slack; if Slack is down we log it and still return
#      200 — because a non-2xx makes Alertmanager retry, and a retry storm on a
#      transient outage duplicates messages and burns model spend without telling
#      the on-call anything new. Getting the alert to Slack is the job; the AI is
#      enhancement on top of it.
#
#   3. Every outbound call is time-bounded. The on-call is waiting on a channel.
#
# Author: Gaurav Chaurasia
###############################################################################

import logging
import os

import requests
from fastapi import FastAPI, Request
from openai import OpenAI

# .env is convenient for local runs; in-cluster the values come from the Secret.
try:
    from dotenv import load_dotenv

    load_dotenv()
except Exception:  # dotenv is optional — the platform injects real env vars
    pass


###############################################################################
# Configuration — everything comes from the environment
###############################################################################

OPENAI_API_KEY = os.getenv("OPENAI_API_KEY")
SLACK_WEBHOOK_URL = os.getenv("SLACK_WEBHOOK_URL")

# Overridable, with the historical defaults.
OPENAI_MODEL = os.getenv("OPENAI_MODEL", "gpt-4o-mini")
OPENAI_TIMEOUT = float(os.getenv("OPENAI_TIMEOUT", "20"))
SLACK_TIMEOUT = float(os.getenv("SLACK_TIMEOUT", "10"))
MAX_TOKENS = int(os.getenv("OPENAI_MAX_TOKENS", "400"))

# Lets the service run (and forward plain alerts) with no model key at all —
# useful for wiring tests that should not spend money or need a key.
AI_ENRICH_ENABLED = os.getenv("AI_ENRICH_ENABLED", "true").lower() != "false"


logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "INFO"),
    format="%(asctime)s %(levelname)s %(message)s",
)
log = logging.getLogger("ai-alertmanager")


app = FastAPI(title="Superhero AI Alertmanager")

# The OpenAI client is built lazily so the process starts (and stays healthy for
# its probes) even when no key is set — enrichment then degrades rather than the
# whole pod crash-looping.
_client = None


def _openai_client():
    global _client
    if _client is None:
        _client = OpenAI(api_key=OPENAI_API_KEY, timeout=OPENAI_TIMEOUT)
    return _client


SYSTEM_PROMPT = """
You are a senior Kubernetes Site Reliability Engineer.

Analyze Kubernetes alerts professionally.

Provide response in this exact format:

🚨 Issue Summary
- short explanation

🔍 Probable Root Cause
- concise root cause

🛠 Recommended Actions
- 3 practical troubleshooting steps

⚡ Suggested Fix
- short remediation advice

Keep response concise, practical, and production-focused.
Maximum 150 words.
"""


###############################################################################
# Payload handling
#
# Alertmanager POSTs a fixed envelope (version 4):
#   { "status": "firing"|"resolved", "alerts": [ {labels, annotations, ...} ],
#     "commonLabels": {...}, "groupLabels": {...}, ... }
# A hand-rolled `curl -d '{...}'` (the way this service was first tested) posts a
# bare object with no "alerts" key. normalize_alerts() accepts both so the old
# manual test keeps working.
###############################################################################


def normalize_alerts(body: dict) -> list[dict]:
    """Return a list of individual alert objects regardless of caller shape."""
    if isinstance(body, dict) and isinstance(body.get("alerts"), list):
        return body["alerts"]
    # Not an Alertmanager envelope — treat the whole object as a single alert.
    return [body]


def summarize_for_model(body: dict) -> str:
    """A compact, readable rendering of the alert group for the model prompt."""
    alerts = normalize_alerts(body)
    status = body.get("status", "firing") if isinstance(body, dict) else "firing"

    lines = [f"Alert group status: {status}", f"Number of alerts: {len(alerts)}", ""]
    for i, alert in enumerate(alerts, 1):
        labels = alert.get("labels", {}) if isinstance(alert, dict) else {}
        annotations = alert.get("annotations", {}) if isinstance(alert, dict) else {}
        lines.append(f"Alert {i}:")
        if labels:
            lines.append(
                "  labels: "
                + ", ".join(f"{k}={v}" for k, v in labels.items())
            )
        if annotations:
            lines.append(
                "  annotations: "
                + ", ".join(f"{k}={v}" for k, v in annotations.items())
            )
        if not labels and not annotations:
            # Bare/manual payload — hand the model whatever was sent.
            lines.append(f"  {alert}")
    return "\n".join(lines)


def alert_headline(body: dict) -> str:
    """A short one-line title for the Slack message, from the common labels."""
    common = body.get("commonLabels", {}) if isinstance(body, dict) else {}
    alerts = normalize_alerts(body)
    first_labels = (
        alerts[0].get("labels", {})
        if alerts and isinstance(alerts[0], dict)
        else {}
    )
    name = common.get("alertname") or first_labels.get("alertname") or "Alert"
    severity = common.get("severity") or first_labels.get("severity") or "unknown"
    namespace = common.get("namespace") or first_labels.get("namespace") or "-"
    status = body.get("status", "firing") if isinstance(body, dict) else "firing"
    emoji = "✅" if status == "resolved" else "🚨"
    return f"{emoji} {name} · severity={severity} · namespace={namespace} · {status}"


###############################################################################
# Enrichment (best-effort)
###############################################################################


def _call_openai(prompt: str) -> str:
    """The one place that talks to OpenAI. Patched out in the offline tests."""
    response = _openai_client().chat.completions.create(
        model=OPENAI_MODEL,
        max_tokens=MAX_TOKENS,
        messages=[
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": f"Analyze this Kubernetes alert:\n\n{prompt}"},
        ],
    )
    return response.choices[0].message.content


def enrich(body: dict) -> tuple[str | None, str | None]:
    """Return (enrichment_text, error). Never raises."""
    if not AI_ENRICH_ENABLED:
        return None, "enrichment disabled (AI_ENRICH_ENABLED=false)"
    if not OPENAI_API_KEY:
        return None, "OPENAI_API_KEY is not set"
    try:
        return _call_openai(summarize_for_model(body)), None
    except Exception as exc:  # any model/network failure degrades, never crashes
        log.warning("OpenAI enrichment failed: %s", exc)
        return None, str(exc)


###############################################################################
# Slack delivery (best-effort)
###############################################################################


def build_slack_message(body: dict, enrichment: str | None, error: str | None) -> str:
    headline = alert_headline(body)
    if enrichment:
        return f"{headline}\n\n{enrichment}"

    # Degraded path — no AI, but the on-call still gets the raw facts.
    detail = summarize_for_model(body)
    note = f"_(AI enrichment unavailable: {error})_" if error else ""
    return f"{headline}\n\n```\n{detail}\n```\n{note}".rstrip()


def send_to_slack(message: str) -> tuple[bool, str | None]:
    """Return (delivered, error). Never raises."""
    if not SLACK_WEBHOOK_URL:
        return False, "SLACK_WEBHOOK_URL is not set"
    try:
        resp = requests.post(
            SLACK_WEBHOOK_URL, json={"text": message}, timeout=SLACK_TIMEOUT
        )
        ok = 200 <= resp.status_code < 300
        if not ok:
            log.warning("Slack returned HTTP %s: %s", resp.status_code, resp.text[:200])
            return False, f"HTTP {resp.status_code}"
        return True, None
    except Exception as exc:
        log.warning("Slack delivery failed: %s", exc)
        return False, str(exc)


###############################################################################
# Routes
###############################################################################


@app.get("/")
def home():
    # Kept for backward compatibility with the original probes.
    return {"message": "Superhero AI Alertmanager is running"}


@app.get("/healthz")
def healthz():
    return {
        "status": "ok",
        "enrich_enabled": AI_ENRICH_ENABLED,
        "openai_key_set": bool(OPENAI_API_KEY),
        "slack_configured": bool(SLACK_WEBHOOK_URL),
        "model": OPENAI_MODEL,
    }


@app.post("/alerts")
async def receive_alert(request: Request):
    # Parse defensively: a body we cannot read is the one case that is genuinely
    # the caller's fault, so it is allowed to be a 400. Everything after this
    # point returns 200 — see rule 2 in the file header.
    try:
        body = await request.json()
    except Exception as exc:
        log.warning("Could not parse request body as JSON: %s", exc)
        return {"status": "error", "reason": f"invalid JSON: {exc}"}

    alerts = normalize_alerts(body)
    log.info("Received %d alert(s): %s", len(alerts), alert_headline(body))

    enrichment, enrich_error = enrich(body)
    message = build_slack_message(body, enrichment, enrich_error)
    delivered, slack_error = send_to_slack(message)

    return {
        "status": "success",
        "alerts": len(alerts),
        "enriched": enrichment is not None,
        "enrich_error": enrich_error,
        "slack_delivered": delivered,
        "slack_error": slack_error,
        "ai_analysis": enrichment,
    }
