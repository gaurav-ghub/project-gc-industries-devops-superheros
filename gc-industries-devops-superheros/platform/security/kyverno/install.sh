#!/usr/bin/env bash

###############################################################################
# File: install.sh
#
# Description:
# Installs Kyverno, the admission-time policy engine of the
# GC Industries Cloud Native Platform.
#
# Responsibilities:
# - Install the Kyverno control plane
# - Wait for the webhooks to become Ready
# - Register the platform's ClusterPolicies with ArgoCD
#
# Philosophy:
# The platform owns policy. Applications consume it.
#
# The `launchpad` CLI evaluates the same policies before a commit is made, so a
# developer learns about a violation while they can still fix it in one line.
# Kyverno is the backstop for anything that reaches the cluster by another
# route — kubectl apply, a hand-edited manifest, a chart the CLI never saw.
#
# Author: Gaurav Chaurasia
###############################################################################

set -euo pipefail

# Each file in this module uses its OWN directory variable. These scripts are
# `source`d, not executed as subprocesses, so a shared name like SECURITY_DIR
# would be silently overwritten by whichever file was sourced last — and the
# parent's next `source "${SECURITY_DIR}/..."` would resolve against the child's
# directory instead of its own.
KYVERNO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${KYVERNO_DIR}/../../scripts/utils.sh"

# Plain assignments, not `readonly`. This file is sourced, and a second source
# in the same shell — re-running the module by hand after a bootstrap, say —
# would abort on "readonly variable" under `set -e`. utils.sh guards against
# that with a load flag; the simpler fix here is not to make them readonly.
KYVERNO_CHART_VERSION="3.8.2"
KYVERNO_NAMESPACE="kyverno"
KYVERNO_POLICIES_APP="${PROJECT_ROOT}/infra/argocd/kyverno-argocd-app.yaml"


install_kyverno() {

    log_info "Installing Kyverno..."

    check_kyverno_repository

    install_kyverno_chart

    wait_for_kyverno_components

    verify_kyverno_installation

    register_kyverno_policies

    log_success "Kyverno installed."

}


check_kyverno_repository() {

    log_info "Checking Kyverno Helm repository..."

    echo

    if helm repo list | grep -q "^kyverno"; then

        log_info "Kyverno Helm repository already exists."

    else

        log_info "Adding Kyverno Helm repository..."

        helm repo add kyverno https://kyverno.github.io/kyverno/

    fi

    echo

    helm repo update kyverno

    echo

    log_success "Kyverno Helm repository is ready."

    echo

}


install_kyverno_chart() {

    log_info "Installing Kyverno chart (${KYVERNO_CHART_VERSION})..."

    echo

    if helm status kyverno \
        --namespace "${KYVERNO_NAMESPACE}" >/dev/null 2>&1; then

        log_warn "Kyverno is already installed."

        echo

        return

    fi

    if helm upgrade \
        --install kyverno \
        kyverno/kyverno \
        --namespace "${KYVERNO_NAMESPACE}" \
        --create-namespace \
        --version "${KYVERNO_CHART_VERSION}" \
        --values "${KYVERNO_DIR}/values.yaml"
    then

        echo

        log_success "Kyverno components installed."

    else

        log_error "Failed to install Kyverno."

        exit 1

    fi

}


wait_for_kyverno_components() {

    log_info "Waiting for Kyverno components to become Ready..."

    echo

    # Until the admission controller is Ready its webhooks are registered but
    # unbacked, and every create in a matched namespace fails with a webhook
    # connection error. Waiting here is what keeps the rest of the bootstrap
    # from tripping over a half-installed policy engine.
    if ! kubectl wait \
        --namespace "${KYVERNO_NAMESPACE}" \
        --for=condition=Ready \
        pod \
        -l app.kubernetes.io/part-of=kyverno \
        --timeout=300s
    then

        log_warn "Some Kyverno pods did not reach Ready state within the timeout."

        kubectl get pods -n "${KYVERNO_NAMESPACE}"

        echo

        log_error "Kyverno components failed to become Ready."

        exit 1

    fi

    echo

    log_success "All Kyverno components are Ready."

    echo

}


verify_kyverno_installation() {

    log_info "Verifying Kyverno installation..."

    echo

    if ! kubectl get crd clusterpolicies.kyverno.io >/dev/null 2>&1; then

        log_error "ClusterPolicy CRD not found — Kyverno did not install correctly."

        exit 1

    fi

    log_success "Kyverno CRDs are registered."

    echo

    kubectl get pods -n "${KYVERNO_NAMESPACE}"

    echo

}


register_kyverno_policies() {

    log_info "Registering platform ClusterPolicies with ArgoCD..."

    echo

    if [[ ! -f "${KYVERNO_POLICIES_APP}" ]]; then

        log_warn "Policy Application manifest not found: ${KYVERNO_POLICIES_APP}"

        log_warn "Skipping policy registration."

        echo

        return

    fi

    if ! kubectl get namespace argocd >/dev/null 2>&1; then

        log_warn "ArgoCD is not installed — skipping policy registration."

        log_warn "Run the GitOps module first, then re-run this installer."

        echo

        return

    fi

    kubectl apply -f "${KYVERNO_POLICIES_APP}"

    echo

    log_success "ClusterPolicies registered with ArgoCD."

    log_info "ArgoCD syncs them from infra/kyverno_policy/ — they must be pushed to be applied."

    echo

}


display_kyverno_policies() {

    print_subsection "Cluster Policies"

    echo

    if kubectl get clusterpolicies >/dev/null 2>&1; then

        kubectl get clusterpolicies

    else

        log_warn "No ClusterPolicies found yet — ArgoCD may still be syncing."

    fi

    echo

}


main() {

    install_kyverno

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
