#!/usr/bin/env bash

###############################################################################
# File: verify.sh
#
# Description:
# Verification for the Access module: the routing objects exist, Kiali is
# serving, and the cluster still publishes the ports every printed URL arrives
# on.
#
# The last of those is the one that matters. Every other check in this file can
# pass on a cluster created before the access layer existed — the Gateway
# applies, the VirtualService applies, Kiali runs — and not one address would
# answer, because kind fixes its port mappings at creation time and this cluster
# was created without them. A module that reported success there would be
# reporting that it had installed something, which is not the question anybody
# is asking.
#
# Author: Gaurav Chaurasia
###############################################################################

# Not `readonly` — this file is sourced, and a second source in the same shell
# would fail (the collision class fixed in Phase 3).
ACCESS_VERIFY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${ACCESS_VERIFY_DIR}/../scripts/utils.sh"


verify_access_installation() {

    log_info "Verifying the access layer..."

    echo

    verify_gateway_object

    verify_dashboard_routes

    verify_kiali_service

    verify_istio_podmonitor

    # The host half. cluster.sh owns this check; the access module is the one
    # that has a reason to care about the answer.
    verify_cluster_port_mappings

    log_success "Access layer verified."

    echo

}


verify_gateway_object() {

    if kubectl get gateway "${GATEWAY_NAME}" -n "${ACCESS_NAMESPACE}" >/dev/null 2>&1; then

        log_success "Gateway ${GATEWAY_NAME} exists."

    else

        log_error "Gateway ${GATEWAY_NAME} was not created."

        exit 1

    fi

}


verify_dashboard_routes() {

    local routes

    if ! kubectl get virtualservice "${DASHBOARDS_NAME}" \
        -n "${ACCESS_NAMESPACE}" >/dev/null 2>&1; then

        log_error "VirtualService ${DASHBOARDS_NAME} was not created."

        exit 1

    fi

    routes="$(kubectl get virtualservice "${DASHBOARDS_NAME}" \
        -n "${ACCESS_NAMESPACE}" \
        -o jsonpath='{range .spec.http[*]}{.match[0].uri.prefix}{" "}{end}' 2>/dev/null)"

    log_success "Platform routes: ${routes}"

}


verify_kiali_service() {

    if kubectl get service kiali -n "${ACCESS_NAMESPACE}" >/dev/null 2>&1; then

        log_success "Kiali service is present."

    else

        log_error "Kiali service was not created."

        exit 1

    fi

}


# verify_istio_podmonitor is 14.10's guard: existing without this would be
# exactly B5's fault again — every other component in this module correct and
# reachable while istio_requests_total never exists because nothing scrapes
# the sidecars.
verify_istio_podmonitor() {

    if kubectl get podmonitor "${ISTIO_PODMONITOR_NAME}" -n "${ACCESS_NAMESPACE}" >/dev/null 2>&1; then

        log_success "PodMonitor ${ISTIO_PODMONITOR_NAME} exists."

    else

        log_error "PodMonitor ${ISTIO_PODMONITOR_NAME} was not created — Kiali's traffic graph has nothing to read."

        exit 1

    fi

}


###############################################################################
# End-of-module summary
#
# Silent under ENDURANCE_FRAMED, like every other display_* in the platform.
# This one is the sharpest case of the rule: it prints the platform's URLs, and
# a module printing URLs mid-run is what Phase 9's live bootstrap exposed —
# Grafana's address announced three modules before Grafana could answer it.
# Endurance prints one Access block, after the run, and probes it first.
###############################################################################

display_access_summary() {

    if framed; then return 0; fi

    print_section "Access"

    echo "The platform is published on the host through the Istio ingress gateway."

    echo

    echo "  http://localhost:8080/argocd"
    echo "  http://localhost:8080/kiali"
    echo "  http://localhost:8080/grafana"
    echo "  http://localhost:8080/prometheus"
    echo "  http://localhost:8080/alertmanager"

    echo

    echo "An application reaches the same gateway by declaring a route in its spec;"
    echo "endurance urls prints what is actually exposed, and checks each address."

    echo

}
