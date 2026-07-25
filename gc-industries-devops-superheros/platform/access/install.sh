#!/usr/bin/env bash

###############################################################################
# File: install.sh
#
# Description:
# Public entry point for the Access module of the Endurance platform.
#
# Purpose:
# Turns the platform from a set of installed components into a set of
# *reachable* ones. Until this module existed, every dashboard was behind a
# `kubectl port-forward` — one terminal per URL, a daemon that dies with the
# shell that started it, and a demo that breaks the moment a laptop sleeps.
#
# Responsibilities:
# - Install Kiali, the mesh console (the one component this module owns)
# - Apply the platform's single ingress Gateway
# - Apply the platform's own routes on it (/argocd /kiali /grafana ...)
# - Prove the cluster actually publishes the ports those routes arrive on
#
# Philosophy:
# The platform owns the front door. An application does not open one of its
# own — it declares a route in its spec, charts/app renders a VirtualService
# bound to this Gateway, and ArgoCD applies it. Nothing here knows an
# application's URL structure, which is the property that keeps the platform
# application-agnostic.
#
# Runs last in the bootstrap chain: every route below points at a component an
# earlier module installed, and a route to a Service that does not exist yet is
# a route that resolves to a 503 the first time anyone looks.
#
# Author: Gaurav Chaurasia
###############################################################################

set -euo pipefail

# Own directory variable, resolved before sourcing any child — these files are
# sourced into one shell by bootstrap-kind.sh, so a name shared with another
# module would be overwritten (the collision class fixed in Phase 3).
ACCESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${ACCESS_DIR}/../scripts/utils.sh"
source "${ACCESS_DIR}/verify.sh"

# One definition, two callers, twice over:
#
#   cluster.sh   owns whether the kind node publishes the platform's ports.
#                It is the file that created the cluster, so it is the file
#                that knows what was asked for.
#   istio/...    owns whether the ingress gateway is published on those node
#                ports. Re-deriving either check here would be a second
#                implementation of a question with one answer.
source "${ACCESS_DIR}/../scripts/cluster.sh"
source "${ACCESS_DIR}/../networking/istio/install.sh"

ACCESS_NAMESPACE="istio-system"
ACCESS_MANIFESTS="${ACCESS_DIR}/manifests"
ACCESS_VERSIONS="${ACCESS_DIR}/versions.yaml"

KIALI_RELEASE="kiali"
KIALI_CHART="kiali/kiali-server"
KIALI_REPO="kiali"
KIALI_REPO_URL="https://kiali.org/helm-charts"
KIALI_VALUES="${ACCESS_DIR}/kiali/values.yaml"

GATEWAY_NAME="endurance-gateway"
DASHBOARDS_NAME="endurance-dashboards"


install_access() {

    log_info "Opening the platform access layer..."

    echo

    require_published_gateway

    install_kiali

    apply_platform_routes

    verify_access_installation

    log_success "Platform access layer ready."

    echo

    display_access_summary

}


###############################################################################
# Preconditions
###############################################################################

# The whole module is routing, and routing needs somewhere for traffic to
# arrive. Failing here — with the reason — beats installing five routes onto a
# gateway nothing can reach and calling it a success.
require_published_gateway() {

    log_info "Checking the ingress gateway is reachable from the host..."

    echo

    if ! verify_ingress_gateway_published; then

        log_error "The access layer needs a published ingress gateway."
        log_error "Run the networking module first: bash platform/networking/install.sh"

        exit 1

    fi

}


###############################################################################
# Kiali
###############################################################################

# declared_kiali_version reads versions.yaml.
#
# sed and not yq, for the reason the Istio module gives: a module must not make
# the platform's prerequisites depend on a tool nothing has checked for.
declared_kiali_version() {

    sed -n '/^kiali:/,/^[^[:space:]#]/p' "${ACCESS_VERSIONS}" \
        | sed -n 's/^[[:space:]]*version:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}[[:space:]]*$/\1/p' \
        | head -1

}


install_kiali() {

    local version

    version="$(declared_kiali_version)"

    if [[ -z "${version}" ]]; then

        log_error "Could not read kiali.version from platform/access/versions.yaml."

        exit 1

    fi

    log_info "Installing Kiali ${version}..."

    echo

    check_kiali_repository

    assert_kiali_chart_version "${version}"

    # --install, not a "skip if present" guard: the Kiali values carry the web
    # root and the Grafana links, so an installed release that was never
    # upgraded is a release that quietly ignores every change to this module.
    # That was a real bug in the ArgoCD installer before Phase 5.
    if helm upgrade \
        --install "${KIALI_RELEASE}" \
        "${KIALI_CHART}" \
        --version "${version}" \
        --namespace "${ACCESS_NAMESPACE}" \
        --values "${KIALI_VALUES}"
    then

        echo

        log_success "Kiali ${version} installed."

    else

        log_error "Failed to install Kiali."

        exit 1

    fi

    wait_for_kiali

}


check_kiali_repository() {

    if helm repo list 2>/dev/null | grep -q "^${KIALI_REPO}[[:space:]]"; then

        log_info "Kiali Helm repository already exists."

    else

        log_info "Adding the Kiali Helm repository..."

        helm repo add "${KIALI_REPO}" "${KIALI_REPO_URL}"

    fi

    helm repo update "${KIALI_REPO}" >/dev/null

    echo

}


# The pinned chart has to exist before helm is asked for it, so that a stale pin
# reports as a stale pin rather than as a Helm error four lines long. Naming the
# versions that do exist is the difference between a one-number fix and a search.
assert_kiali_chart_version() {

    local want="$1"

    if helm search repo "${KIALI_CHART}" --version "${want}" 2>/dev/null | grep -q "${want}"; then

        log_success "Kiali chart ${want} is available."

        echo

        return 0

    fi

    log_error "Kiali chart version ${want} was not found in the ${KIALI_REPO} repository."

    echo

    echo "platform/access/versions.yaml pins kiali.version. Available versions:"

    helm search repo "${KIALI_CHART}" --versions 2>/dev/null | head -6

    echo

    exit 1

}


wait_for_kiali() {

    log_info "Waiting for Kiali to become Ready..."

    echo

    if ! kubectl rollout status \
        "deployment/${KIALI_RELEASE}" \
        -n "${ACCESS_NAMESPACE}" \
        --timeout=180s; then

        kubectl get pods -n "${ACCESS_NAMESPACE}" -l "app.kubernetes.io/name=kiali"

        echo

        log_error "Kiali did not become Ready within the timeout."

        exit 1

    fi

    echo

    log_success "Kiali is Ready."

    echo

}


###############################################################################
# Routes
###############################################################################

apply_platform_routes() {

    log_info "Applying the platform ingress routes..."

    echo

    kubectl apply -f "${ACCESS_MANIFESTS}/gateway.yaml"

    kubectl apply -f "${ACCESS_MANIFESTS}/dashboards.yaml"

    echo

    log_success "Ingress Gateway and platform routes applied."

    echo

}


main() {

    install_access

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
