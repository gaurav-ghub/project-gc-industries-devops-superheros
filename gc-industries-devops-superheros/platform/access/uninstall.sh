#!/usr/bin/env bash

###############################################################################
# File: uninstall.sh
#
# Description:
# Removes the access layer: the platform's routes, its Gateway, and Kiali.
#
# It does not touch the ingress gateway itself. That is Istio's, installed by
# the networking module, and removing it here would take every application's
# route down with it — an application's VirtualService binds to the Gateway this
# file deletes, and the Gateway is the only thing in that chain the access
# module owns.
#
# Author: Gaurav Chaurasia
###############################################################################

set -euo pipefail

ACCESS_UNINSTALL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${ACCESS_UNINSTALL_DIR}/../scripts/utils.sh"

ACCESS_NAMESPACE="istio-system"


uninstall_access() {

    log_info "Removing the platform access layer..."

    kubectl delete virtualservice endurance-dashboards \
        -n "${ACCESS_NAMESPACE}" --ignore-not-found

    kubectl delete gateway endurance-gateway \
        -n "${ACCESS_NAMESPACE}" --ignore-not-found

    if helm status kiali --namespace "${ACCESS_NAMESPACE}" >/dev/null 2>&1; then

        helm uninstall kiali --namespace "${ACCESS_NAMESPACE}"

    else

        log_info "Kiali is not installed."

    fi

    log_success "Access layer removed. Applications keep their routes, and nothing serves them."

}


main() {

    uninstall_access

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
