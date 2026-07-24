#!/usr/bin/env bash

###############################################################################
# File: cluster.sh
#
# Description:
# The cluster half of the Endurance bootstrap: validate the operator's local
# tooling, create the kind cluster when it is missing, and prove the API server
# is answering before any platform module tries to install into it.
#
# Why this is its own file:
# `endurance bootstrap` runs each platform module as a separate subprocess so
# that every module is one step in the CLI's progress chain. Creating the
# cluster is the first of those steps, and it used to be defined inside
# bootstrap-kind.sh — where the CLI could not reach it without running the
# entire bootstrap. Extracting it keeps exactly one definition of how an
# Endurance cluster comes into existence: bootstrap-kind.sh sources this file,
# and the CLI runs it directly.
#
# Output is plain facts, as everywhere else in the platform plumbing: the Go
# renderer frames it (see platform/lib/logger.sh and the ENDURANCE_FRAMED
# contract).
#
# Author: Gaurav Chaurasia
###############################################################################

CLUSTER_SH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${CLUSTER_SH_DIR}/utils.sh"


###############################################################################
# Local Environment Validation
###############################################################################

validate_local_environment() {

    log_info "Validating local environment..."

    check_command docker
    check_command kubectl
    check_command helm
    check_command kind
    check_command git

    log_success "Local environment validation completed."

}


###############################################################################
# Cluster
###############################################################################

cluster_exists() {

    if kind get clusters | grep -q "^${CLUSTER_NAME}$"; then
        return 0
    fi

    return 1
}

create_kind_cluster() {

    log_info "Creating Kind cluster ${CLUSTER_NAME}..."

    if ! kind create cluster \
        --name "${CLUSTER_NAME}" \
        --config "${KIND_CONFIG}"; then

        log_error "Failed to create Kind cluster."
        exit 1
    fi

    log_success "Kind cluster created successfully."
}


###############################################################################
# Cluster Validation
###############################################################################

validate_cluster() {

    log_info "Validating Kubernetes cluster..."

    check_kubernetes_context "${KUBERNETES_CONTEXT}"

    verify_kind_cluster

    check_cluster_ready

    log_success "Cluster validation completed."

}


bootstrap_cluster() {

    if cluster_exists; then

        log_info "Kind cluster ${CLUSTER_NAME} already exists."

    else

        create_kind_cluster

    fi

    wait_for_cluster

    validate_cluster

}


main() {

    validate_local_environment

    bootstrap_cluster

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
