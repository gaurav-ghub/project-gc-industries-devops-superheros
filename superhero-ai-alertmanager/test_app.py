###############################################################################
# File: test_app.py
#
# Offline tests for the AI alertmanager. Nothing here touches OpenAI or Slack —
# the one function that calls OpenAI (`_call_openai`) and the HTTP client used to
# reach Slack (`requests.post`) are stubbed. So this runs with no API key, no
# network, and no spend, and still proves the two things that matter:
#
#   * a real Alertmanager envelope is parsed, enriched and delivered, and
#   * every failure mode still returns 200 and still gets the alert to Slack
#     (or, if Slack itself is down, reports that without a retry-triggering 5xx).
#
# Run:  python -m unittest test_app -v
###############################################################################

import unittest
from unittest import mock

from fastapi.testclient import TestClient

import app as appmod

client = TestClient(appmod.app)


# A representative Alertmanager v4 webhook body for the platform's pod-restart rule.
ALERTMANAGER_BODY = {
    "status": "firing",
    "receiver": "ai-webhook",
    "groupLabels": {"alertname": "PodRestartingFrequently", "namespace": "superheros"},
    "commonLabels": {
        "alertname": "PodRestartingFrequently",
        "namespace": "superheros",
        "severity": "warning",
        "team": "platform",
    },
    "commonAnnotations": {"summary": "Superhero application pod restarting"},
    "alerts": [
        {
            "status": "firing",
            "labels": {
                "alertname": "PodRestartingFrequently",
                "namespace": "superheros",
                "pod": "payment-abc123",
                "severity": "warning",
            },
            "annotations": {"description": "Pod payment-abc123 restarted in namespace superheros"},
        }
    ],
}


class FakeSlack:
    """Records the last payload posted to Slack and returns a canned status."""

    def __init__(self, status_code=200):
        self.status_code = status_code
        self.text = "ok"
        self.last_json = None

    def __call__(self, url, json=None, timeout=None):
        self.last_json = json
        return self


class HealthTests(unittest.TestCase):
    def test_root_ok(self):
        r = client.get("/")
        self.assertEqual(r.status_code, 200)
        self.assertIn("running", r.json()["message"])

    def test_healthz(self):
        r = client.get("/healthz")
        self.assertEqual(r.status_code, 200)
        self.assertEqual(r.json()["status"], "ok")
        self.assertIn("model", r.json())


class ParsingTests(unittest.TestCase):
    def test_normalize_envelope(self):
        self.assertEqual(len(appmod.normalize_alerts(ALERTMANAGER_BODY)), 1)

    def test_normalize_bare_payload(self):
        # A hand-rolled curl body with no "alerts" key is treated as one alert.
        self.assertEqual(appmod.normalize_alerts({"pod": "x"}), [{"pod": "x"}])

    def test_headline_uses_common_labels(self):
        head = appmod.alert_headline(ALERTMANAGER_BODY)
        self.assertIn("PodRestartingFrequently", head)
        self.assertIn("severity=warning", head)
        self.assertIn("namespace=superheros", head)
        self.assertTrue(head.startswith("🚨"))

    def test_summary_lists_pod(self):
        summary = appmod.summarize_for_model(ALERTMANAGER_BODY)
        self.assertIn("payment-abc123", summary)
        self.assertIn("Number of alerts: 1", summary)


class HappyPathTests(unittest.TestCase):
    def test_enriched_and_delivered(self):
        slack = FakeSlack(200)
        with mock.patch.object(appmod, "OPENAI_API_KEY", "test-key"), \
             mock.patch.object(appmod, "SLACK_WEBHOOK_URL", "https://slack.example/x"), \
             mock.patch.object(appmod, "AI_ENRICH_ENABLED", True), \
             mock.patch.object(appmod, "_call_openai", return_value="🚨 Issue Summary\n- pod is restarting"), \
             mock.patch.object(appmod.requests, "post", slack):
            r = client.post("/alerts", json=ALERTMANAGER_BODY)

        self.assertEqual(r.status_code, 200)
        data = r.json()
        self.assertEqual(data["status"], "success")
        self.assertTrue(data["enriched"])
        self.assertTrue(data["slack_delivered"])
        # The message actually posted to Slack carried both the headline and the AI text.
        self.assertIn("PodRestartingFrequently", slack.last_json["text"])
        self.assertIn("Issue Summary", slack.last_json["text"])


class DegradedPathTests(unittest.TestCase):
    def test_openai_down_still_delivers_raw(self):
        slack = FakeSlack(200)
        with mock.patch.object(appmod, "OPENAI_API_KEY", "test-key"), \
             mock.patch.object(appmod, "SLACK_WEBHOOK_URL", "https://slack.example/x"), \
             mock.patch.object(appmod, "AI_ENRICH_ENABLED", True), \
             mock.patch.object(appmod, "_call_openai", side_effect=RuntimeError("model timeout")), \
             mock.patch.object(appmod.requests, "post", slack):
            r = client.post("/alerts", json=ALERTMANAGER_BODY)

        self.assertEqual(r.status_code, 200)  # never 5xx — no Alertmanager retry storm
        data = r.json()
        self.assertFalse(data["enriched"])
        self.assertIn("model timeout", data["enrich_error"])
        self.assertTrue(data["slack_delivered"])
        # Degraded message still carries the raw facts and says enrichment failed.
        self.assertIn("payment-abc123", slack.last_json["text"])
        self.assertIn("AI enrichment unavailable", slack.last_json["text"])

    def test_no_key_degrades(self):
        slack = FakeSlack(200)
        with mock.patch.object(appmod, "OPENAI_API_KEY", None), \
             mock.patch.object(appmod, "SLACK_WEBHOOK_URL", "https://slack.example/x"), \
             mock.patch.object(appmod.requests, "post", slack):
            r = client.post("/alerts", json=ALERTMANAGER_BODY)
        self.assertEqual(r.status_code, 200)
        self.assertFalse(r.json()["enriched"])
        self.assertTrue(r.json()["slack_delivered"])

    def test_slack_down_returns_200(self):
        slack = FakeSlack(500)
        with mock.patch.object(appmod, "OPENAI_API_KEY", "test-key"), \
             mock.patch.object(appmod, "SLACK_WEBHOOK_URL", "https://slack.example/x"), \
             mock.patch.object(appmod, "_call_openai", return_value="enriched"), \
             mock.patch.object(appmod.requests, "post", slack):
            r = client.post("/alerts", json=ALERTMANAGER_BODY)
        # Slack failing must not fail the webhook — that would make Alertmanager retry.
        self.assertEqual(r.status_code, 200)
        self.assertFalse(r.json()["slack_delivered"])
        self.assertEqual(r.json()["slack_error"], "HTTP 500")


if __name__ == "__main__":
    unittest.main()
