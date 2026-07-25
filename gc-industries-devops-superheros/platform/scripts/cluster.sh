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


###############################################################################
# Published ports — the host half of the access layer
#
# kind fixes extraPortMappings at cluster-creation time. A cluster created
# before kind-config.yaml declared them comes up perfectly healthy and cannot
# be reached from the host at all, so every address Endurance prints would be
# a dead link while every module reported success.
#
# One definition, two callers, as with cluster creation itself: the access
# module sources this file to make the same check after it has installed the
# routes.
###############################################################################

# declared_port_mappings emits "containerPort hostPort" for each mapping in
# kind-config.yaml. The two keys always appear in that order, one pair per
# entry, which is what makes this readable without a YAML processor.
declared_port_mappings() {

    awk '
        $1 == "-" && $2 == "containerPort:" { container = $3 ; next }
        $1 == "containerPort:"              { container = $2 ; next }
        $1 == "hostPort:" && container != "" { print container, $2 ; container = "" }
    ' "${KIND_CONFIG}"

}


# cluster_publishes_ports reports whether the node container actually publishes
# every mapping kind-config.yaml declares. Quiet: callers decide what to say.
cluster_publishes_ports() {

    local node="${CLUSTER_NAME}-control-plane"
    local container host

    while read -r container host; do

        [[ -z "${container}" ]] && continue

        if [[ -z "$(docker port "${node}" "${container}/tcp" 2>/dev/null)" ]]; then

            return 1

        fi

    done < <(declared_port_mappings)

    return 0

}


verify_cluster_port_mappings() {

    log_info "Checking the cluster publishes the platform's ports..."

    local node="${CLUSTER_NAME}-control-plane"
    local container host published missing=0

    while read -r container host; do

        [[ -z "${container}" ]] && continue

        published="$(docker port "${node}" "${container}/tcp" 2>/dev/null | head -1)"

        if [[ -z "${published}" ]]; then

            log_warn "Node ${node} does not publish container port ${container} (host ${host})."

            missing=1

        else

            log_info "host ${host} maps to node port ${container} (${published})."

        fi

    done < <(declared_port_mappings)

    if [[ "${missing}" -ne 0 ]]; then

        log_warn "This cluster was created before kind-config.yaml declared those mappings."
        log_warn "kind fixes them at creation time, so the cluster has to be recreated:"
        log_warn "  endurance destroy   then   endurance bootstrap"
        log_warn "Until then the platform installs correctly and none of its URLs answer."

        return 0

    fi

    log_success "The cluster publishes every declared port mapping."

}


bootstrap_cluster() {

    if cluster_exists; then

        log_info "Kind cluster ${CLUSTER_NAME} already exists."

    else

        create_kind_cluster

    fi

    wait_for_cluster

    validate_cluster

    verify_cluster_port_mappings

}


main() {

    validate_local_environment

    bootstrap_cluster

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
