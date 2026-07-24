#!/usr/bin/env bash

###############################################################################
# File: destroy-kind.sh
#
# Description:
# Tears the local Endurance platform down by deleting the kind cluster it runs
# on. Everything the platform installed — Istio, monitoring, the AI enricher,
# ArgoCD, Kyverno and every onboarded application — lives inside that cluster,
# so deleting it is the whole teardown. Nothing in git is touched.
#
# Deliberately not a per-module uninstall chain: the modules each have their
# own uninstall.sh for the operator who wants to remove one capability from a
# running cluster. When the answer is "remove all of it", uninstalling six
# modules in reverse order is six ways to leave something behind, and the
# cluster is disposable by design.
#
# Re-runnable: a cluster that is already gone is not an error.
#
# This is plumbing. `endurance destroy` is the front door and frames this
# script's output through the Go renderer.
#
# Author: Gaurav Chaurasia
###############################################################################

DESTROY_SH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${DESTROY_SH_DIR}/utils.sh"


cluster_present() {

    kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"

}


delete_kind_cluster() {

    log_info "Deleting Kind cluster ${CLUSTER_NAME}..."

    if ! kind delete cluster --name "${CLUSTER_NAME}"; then

        log_error "Failed to delete Kind cluster ${CLUSTER_NAME}."
        exit 1

    fi

    log_success "Kind cluster ${CLUSTER_NAME} deleted."

}


# kind removes its own kubeconfig entries on delete. This is the safety net for
# the case it could not — a half-deleted cluster leaving a context behind means
# the next `kubectl` silently talks to nothing.
remove_stale_context() {

    if kubectl config get-contexts "${KUBERNETES_CONTEXT}" >/dev/null 2>&1; then

        log_info "Removing leftover kubectl context ${KUBERNETES_CONTEXT}..."

        kubectl config delete-context "${KUBERNETES_CONTEXT}" >/dev/null 2>&1 || true
        kubectl config delete-cluster "${KUBERNETES_CONTEXT}" >/dev/null 2>&1 || true

    fi

    log_success "No Endurance kubectl context remains."

}


destroy_cluster() {

    if ! cluster_present; then

        log_info "Kind cluster ${CLUSTER_NAME} does not exist — nothing to delete."

        remove_stale_context

        return

    fi

    delete_kind_cluster

    remove_stale_context

}


main() {

    log_info "Destroying the local Endurance platform..."

    # Without kind, `kind get clusters` fails and the teardown would report
    # "nothing to delete" about a cluster it simply cannot see.
    check_command kind

    destroy_cluster

    log_success "Local platform teardown completed."

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
