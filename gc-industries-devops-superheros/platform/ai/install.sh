#!/usr/bin/env bash

###############################################################################
# File: install.sh
#
# Description:
# Public entry point for the AI module of the
# Endurance.
#
# Purpose:
# Deploys the Superhero AI Alertmanager — the enrichment hop that sits between
# Prometheus Alertmanager and Slack. Alertmanager routes matching alerts to this
# service; it asks OpenAI to explain them like an SRE would and posts the
# enriched summary to Slack.
#
# Responsibilities:
# - Deploy the enrichment service into the `monitoring` namespace
# - Apply its credentials Secret when present (never committed to git)
# - Verify the service is Ready
#
# Philosophy:
# The platform owns observability, including how alerts reach humans.
# Applications only produce alerts; the platform enriches and delivers them.
#
# The Alertmanager route that feeds this service lives in the monitoring
# module's values (always consumed). Alertmanager holds no Slack or model
# credential — only the URL of this in-cluster service. The credentials live in
# this pod's Secret.
#
# Author: Gaurav Chaurasia
###############################################################################

set -euo pipefail

# Own directory variable, resolved before sourcing any child. These files are
# sourced into one shell, so a name shared with another module would be
# overwritten (the collision class fixed across the platform in Phase 3).
AI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${AI_DIR}/../scripts/utils.sh"
source "${AI_DIR}/verify.sh"

AI_NAMESPACE="monitoring"
AI_DEPLOYMENT="superhero-ai-alertmanager"
AI_SECRET_NAME="superhero-ai-secret"
AI_MANIFESTS="${AI_DIR}/manifests"
AI_SECRET_FILE="${AI_DIR}/secret.yaml"


install_ai() {

    log_info "Installing Platform AI (alert enrichment)..."

    echo

    assert_image_pin

    ensure_monitoring_namespace_present

    ensure_ai_secret

    apply_ai_manifests

    wait_for_ai

    verify_ai_installation

    log_success "Platform AI installed."

    echo

    display_ai_summary

}


# The version file is read, not decoration: assert the tag it declares is the tag
# the deployment actually references, so the two cannot drift.
assert_image_pin() {

    local declared referenced

    declared="$(sed -n 's/^[[:space:]]*tag:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}[[:space:]]*$/\1/p' \
        "${AI_DIR}/versions.yaml" | head -1)"

    if [[ -z "${declared}" ]]; then
        log_warn "Could not read the image tag from versions.yaml — skipping the pin check."
        return
    fi

    if grep -q ":${declared}\$" "${AI_MANIFESTS}/deployment.yaml" || \
       grep -q ":${declared}\"" "${AI_MANIFESTS}/deployment.yaml"; then
        log_success "Image pin ${declared} matches deployment.yaml."
    else
        log_warn "versions.yaml declares tag ${declared} but deployment.yaml references a different tag."
        log_warn "Update one so they agree — the platform installs exactly the image it documents."
    fi

    echo

}


ensure_monitoring_namespace_present() {

    if kubectl get namespace "${AI_NAMESPACE}" >/dev/null 2>&1; then

        log_info "Namespace ${AI_NAMESPACE} present."

        return

    fi

    log_warn "Namespace ${AI_NAMESPACE} not found — run the monitoring module first."

    log_info "Creating it so the AI module can proceed."

    kubectl create namespace "${AI_NAMESPACE}"

}


# The credentials Secret is deliberately NOT in git. Apply the untracked
# secret.yaml when it exists; otherwise leave any existing Secret alone, and if
# there is none, warn — the service still runs (the Secret reference is optional)
# and simply forwards un-enriched alerts until the Secret is created.
ensure_ai_secret() {

    log_info "Checking AI credentials Secret..."

    echo

    if [[ -f "${AI_SECRET_FILE}" ]]; then

        log_info "Applying ${AI_SECRET_FILE} (git-ignored)."

        kubectl apply -f "${AI_SECRET_FILE}"

        if grep -q '<your-' "${AI_SECRET_FILE}"; then

            log_warn "secret.yaml still contains placeholder values."
            log_warn "Enrichment and Slack delivery will not work until you fill them in."

        fi

    elif kubectl get secret "${AI_SECRET_NAME}" -n "${AI_NAMESPACE}" >/dev/null 2>&1; then

        log_info "Secret ${AI_SECRET_NAME} already exists — leaving it as-is."

    else

        log_warn "No ${AI_SECRET_NAME} Secret found and no secret.yaml to apply."
        log_warn "Copy secret.example.yaml to secret.yaml, fill it in, and re-run —"
        log_warn "or create the Secret with kubectl. The service will run either way,"
        log_warn "but it cannot reach OpenAI or Slack until the Secret exists."

    fi

    echo

}


apply_ai_manifests() {

    log_info "Applying AI alertmanager manifests..."

    echo

    kubectl apply -f "${AI_MANIFESTS}/deployment.yaml"

    kubectl apply -f "${AI_MANIFESTS}/service.yaml"

    echo

    log_success "AI alertmanager manifests applied."

    echo

}


wait_for_ai() {

    log_info "Waiting for the AI alertmanager to become Ready..."

    echo

    if ! kubectl rollout status \
        "deployment/${AI_DEPLOYMENT}" \
        -n "${AI_NAMESPACE}" \
        --timeout=180s; then

        log_warn "The AI alertmanager did not become Ready within the timeout."

        kubectl get pods -n "${AI_NAMESPACE}" -l "app=${AI_DEPLOYMENT}"

        echo

        log_error "AI alertmanager failed to become Ready."

        exit 1

    fi

    echo

    log_success "AI alertmanager is Ready."

    echo

}


main() {

    install_ai

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
