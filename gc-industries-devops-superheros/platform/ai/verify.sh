#!/usr/bin/env bash

###############################################################################
# File: verify.sh
#
# Description:
# Verification and summary output for the AI module of the
# GC Industries Cloud Native Platform.
#
# Author: Gaurav Chaurasia
###############################################################################

set -euo pipefail

# Own directory variable — see the note in install.sh.
AI_VERIFY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${AI_VERIFY_DIR}/../scripts/utils.sh"

AI_VERIFY_NAMESPACE="monitoring"
AI_VERIFY_DEPLOYMENT="superhero-ai-alertmanager"


verify_ai_installation() {

    log_info "Verifying AI alertmanager installation..."

    echo

    if ! kubectl get deployment "${AI_VERIFY_DEPLOYMENT}" \
        -n "${AI_VERIFY_NAMESPACE}" >/dev/null 2>&1; then

        log_error "Deployment ${AI_VERIFY_DEPLOYMENT} not found."

        exit 1

    fi

    if ! kubectl get service "${AI_VERIFY_DEPLOYMENT}" \
        -n "${AI_VERIFY_NAMESPACE}" >/dev/null 2>&1; then

        log_error "Service ${AI_VERIFY_DEPLOYMENT} not found."

        exit 1

    fi

    kubectl get pods -n "${AI_VERIFY_NAMESPACE}" -l "app=${AI_VERIFY_DEPLOYMENT}"

    echo

    log_success "AI alertmanager installation verified."

    echo

}


display_ai_summary() {

    print_section "AI Alert Enrichment Summary"

    echo "✓ Superhero AI Alertmanager deployed (namespace: ${AI_VERIFY_NAMESPACE})"

    echo "✓ Alertmanager routes matching alerts to it; it enriches via OpenAI and posts to Slack"

    echo

    echo "Status : READY ✅"

    echo

    print_subsection "The Path"

    echo

    echo "Prometheus rule fires"
    echo "  -> Alertmanager groups + routes to the ai-webhook receiver"
    echo "  -> http://${AI_VERIFY_DEPLOYMENT}.${AI_VERIFY_NAMESPACE}.svc.cluster.local:8000/alerts"
    echo "  -> OpenAI enrichment (SRE-style summary, root cause, actions, fix)"
    echo "  -> Slack incoming webhook"

    echo

    echo "Alertmanager holds no Slack or model credential — only this service's URL."
    echo "The credentials live in the ${AI_VERIFY_DEPLOYMENT} pod's Secret."

    echo

    print_subsection "Useful Commands"

    echo

    echo "Service health"
    echo "kubectl port-forward svc/${AI_VERIFY_DEPLOYMENT} -n ${AI_VERIFY_NAMESPACE} 8000:8000  # then GET /healthz"

    echo

    echo "Enricher logs"
    echo "kubectl logs -n ${AI_VERIFY_NAMESPACE} deploy/${AI_VERIFY_DEPLOYMENT} -f"

    echo

    echo "Confirm the Alertmanager route"
    echo "kubectl port-forward svc/prometheus-kube-prometheus-alertmanager -n monitoring 9093:9093  # UI: http://localhost:9093"

    echo

}
