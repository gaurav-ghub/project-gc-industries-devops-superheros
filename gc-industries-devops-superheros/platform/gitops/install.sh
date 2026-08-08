###############################################################################
# File: install.sh
#
# Description:
# Public entry point for the GitOps module of the
# Endurance.
#
# Purpose:
# This script installs all GitOps capabilities required
# by the platform.
#
# Responsibilities:
# - Install GitOps Tools
# - Verify GitOps Components
# - Register the platform's own ArgoCD Applications
# - Prepare platform GitOps for customer applications
#
# Philosophy:
# The platform owns GitOps.
# Customer applications consume GitOps capabilities.
#
# Author: Gaurav Chaurasia
###############################################################################

#!/usr/bin/env bash

set -euo pipefail

GITOPS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GITOPS_DIR}/../scripts/utils.sh"

source "${GITOPS_DIR}/argocd/install.sh"
source "${GITOPS_DIR}/verify.sh"

# Plain assignment, not `readonly`: this file is sourced, and re-running the
# module by hand in the same shell would abort on "readonly variable".
ALERT_RULES_APP="${PROJECT_ROOT}/infra/argocd/monitoring-rules-argocd-app.yaml"


install_gitops() {

    log_info "Installing Platform GitOps..."

    echo

    install_argocd

    register_platform_alert_rules

    log_success "Platform GitOps installed."

    echo

    display_gitops_summary

}

# Registers the platform's PrometheusRules with ArgoCD.
#
# # Why this lives in the GitOps module and not the monitoring one
#
# The rules are monitoring's, and monitoring would be the obvious home. It
# cannot be: the bootstrap chain is cluster -> Istio -> monitoring -> AI ->
# GitOps -> security -> access, and the monitoring module runs two steps before
# ArgoCD exists. Registration needs ArgoCD, so it happens in the module that
# installs it — the same reason the security module registers the Kyverno
# ClusterPolicies rather than some earlier one.
#
# # Why registration rather than `kubectl apply` of the rule itself
#
# Because Phase 13 exists to fix a rule that nothing applied, and the way that
# stayed invisible for twelve phases is that there was no screen anywhere that
# would have shown its absence. An ArgoCD Application is that screen.
register_platform_alert_rules() {

    log_info "Registering the platform's alert rules with ArgoCD..."

    echo

    if [[ ! -f "${ALERT_RULES_APP}" ]]; then

        log_warn "Alert rules Application manifest not found: ${ALERT_RULES_APP}"

        log_warn "Skipping alert rule registration — the platform will install with no alerting."

        echo

        return

    fi

    kubectl apply -f "${ALERT_RULES_APP}"

    echo

    log_success "Platform alert rules registered with ArgoCD."

    log_info "ArgoCD syncs them from infra/monitoring/rules/ — they must be pushed to be applied."

    echo

}

main() {

    install_gitops

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
