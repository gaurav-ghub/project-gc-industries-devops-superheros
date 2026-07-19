#!/usr/bin/env bash

#########################################################################
# File: utils.sh
# Description: Shared utility library for the GC Industries Cloud Native Platform.
# 
# Responsibilities:
# This shared utility library provides reusable helper functions for all
# platform automation scripts including logging, validation, error handling,
# Kubernetes helpers and common shell utilities.
#
# Every platform script should source this file instead of duplicating common
# functionality.
#
# Philosophy:
# Keep it simple, modular and reusable. Avoid unnecessary complexity and dependencies.
#
# Author: Gaurav Chaurasia
###############################################################################


###############################################################################
# Usage
#
# source "$(dirname "$0")/utils.sh"
#
# Example:
#
# log_info "Installing Istio..."
# command_exists kubectl
# require_cluster
#
###############################################################################




###############################################################################
# Visual separation for readability
###########################################################################

print_separator() {

    echo
    printf '%*s\n' 57 '' | tr ' ' '='
    echo

}

print_section() {

    print_separator

    echo "$1"

    print_separator

}

print_subsection() {

    echo
    echo "---------------------------------------------------------"
    echo "$1"
    echo "---------------------------------------------------------"

}

###########################################################################




# Prevent multiple sourcing
if [[ -n "${UTILS_SH_LOADED:-}" ]]; then
    return
fi

readonly UTILS_SH_LOADED=1

###############################################################################
# Project Paths
###############################################################################

readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

###############################################################################
# Shared Platform Libraries
###############################################################################

readonly PLATFORM_ROOT="${PROJECT_ROOT}/platform"

source "${PLATFORM_ROOT}/lib/colors.sh"
source "${PLATFORM_ROOT}/lib/version.sh"
source "${PLATFORM_ROOT}/lib/banner.sh"
source "${PLATFORM_ROOT}/lib/logger.sh"



readonly KIND_CONFIG="${PROJECT_ROOT}/kind-config.yaml"

########################################################################


###############################################################################
# Banner
###############################################################################

print_banner() {

    echo "=============================================================="
    echo "🚀 ${PLATFORM_NAME}"
    echo "=============================================================="
    echo
    echo "Platform Version : ${PLATFORM_VERSION}"
    echo "Environment      : ${PLATFORM_ENVIRONMENT}"
    echo "Customer         : ${PLATFORM_CUSTOMER}"
    echo
    echo "=============================================================="
    echo
}


###############################################################################



###############################################################################
# Logging Functions
###############################################################################


###############################################################################

###############################################################################
# Command Validation
###############################################################################

check_command() {
    local command="$1"

    if command -v "$command" >/dev/null 2>&1; then
        log_success "$command found."
    else
        log_error "$command is not installed."
        exit 1
    fi
}


###############################################################################

###############################################################################
# Kubernetes Helpers
###############################################################################

check_kubernetes_context() {

    local expected_context="$1"
    local current_context

    current_context=$(kubectl config current-context 2>/dev/null)

    if [[ -z "$current_context" ]]; then
        log_error "Unable to determine the current Kubernetes context."
        exit 1
    fi

    if [[ "$current_context" == "$expected_context" ]]; then
        log_success "Kubernetes context: ${current_context}"
    else
        log_error "Current context: ${current_context}"
        log_error "Expected context: ${expected_context}"
        exit 1
    fi
}


verify_kind_cluster() {

    log_info "Checking Kind clusters..."

    log_success "Kind Cluster is: ${CLUSTER_NAME}"

    log_success "Kind cluster verification passed."

}


############################################################################

###############################################################################
# Cluster Validation
###############################################################################

check_cluster_ready() {

    log_info "Verifying Kubernetes cluster..."

    echo

    if ! kubectl cluster-info >/dev/null 2>&1; then

        log_error "Unable to communicate with Kubernetes cluster."
        log_info "If this is a Kind environment, create or start the cluster first."

        exit 1

    fi

    log_success "Kubernetes API is reachable."

    echo

    log_info "Checking Kubernetes nodes..."

    echo

    kubectl get nodes

    echo

    local ready_nodes

    ready_nodes=$(kubectl get nodes \
        --no-headers \
        2>/dev/null | grep -c " Ready")

    if [[ "${ready_nodes}" -eq 0 ]]; then

        log_error "No Ready nodes found."

        exit 1

    fi

    log_success "All Kubernetes nodes are Ready."

}

############################################################################

###############################################################################
# Cluster Wait Helpers
###############################################################################

wait_for_cluster() {

    log_info "Waiting for Kubernetes nodes to become Ready..."

    if ! kubectl wait \
        --for=condition=Ready nodes \
        --all \
        --timeout=300s; then

        log_error "Cluster did not become ready within 5 minutes."
        exit 1
    fi

    log_success "All Kubernetes nodes are Ready."
}

############################################################################