#!/usr/bin/env bash

###############################################################################
# File: bootstrap-kind.sh
#
# Description:
# Bootstraps the complete GC Industries Cloud Native Platform
# on a local Kind Kubernetes cluster.
#
# Responsibilities:
#   - Validate platform prerequisites
#   - Create Kind cluster
#   - Verify cluster health
#   - (Later) Install Istio
#   - (Later) Install Monitoring
#   - (Later) Install Kiali
#   - (Later) Install ArgoCD
#   - (Later) Deploy SuperHeroes application
#
# Author: Gaurav Chaurasia
###############################################################################


SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/utils.sh"
source "${PROJECT_ROOT}/platform/networking/install.sh"

cluster_exists() {

    if kind get clusters | grep -q "^superheros$"; then
        return 0
    fi

    return 1
}

create_kind_cluster() {

    log_info "Creating Kind cluster..."

    kind create cluster \
        --name superheros \
        --config "${KIND_CONFIG}"

    if [ $? -ne 0 ]; then
        log_error "Failed to create Kind cluster."
        exit 1
    fi

    log_success "Kind cluster created successfully."
}



###############################################################################
# Local Environment Validation
###############################################################################

validate_local_environment() {

    log_info "Validating local environment..."

    echo

    check_command docker
    check_command kubectl
    check_command helm
    check_command kind
    check_command git

    echo

    log_success "Local environment validation completed."

}


###############################################################################
# Cluster Validation
###############################################################################

validate_cluster() {

    log_info "Validating Kubernetes cluster..."

    echo

    check_kubernetes_context "${KUBERNETES_CONTEXT}"

    echo

    verify_kind_cluster

    check_cluster_ready

    echo

    log_success "Cluster validation completed."

}



bootstrap_cluster() {

    if cluster_exists; then

        log_warn "Kind cluster already exists."

    else

        create_kind_cluster

    fi

    wait_for_cluster

    validate_cluster

}

main() {

    print_banner

    validate_local_environment

    bootstrap_cluster

    echo

    install_networking

    echo

    log_success "Local platform bootstrap completed successfully."

}


main
