#!/usr/bin/env bash

###############################################################################
# File: install.sh
#
# Description:
# Public entry point for the Networking module of the
# GC Industries Cloud Native Platform.
#
# Purpose:
# This script installs all networking capabilities required
# by the platform.
#
# Responsibilities:
# - Install Service Mesh
# - Verify Networking Components
# - Prepare platform networking for customer applications
#
# Philosophy:
# The platform owns networking.
# Customer applications consume networking capabilities.
#
# Author: Gaurav Chaurasia
###############################################################################


# Not `readonly`: this file is sourced, and a second source in the same shell
# fails with "readonly variable" — silently noisy on its own, and fatal once
# another module has turned on `set -e` in the shared shell.
NETWORKING_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${NETWORKING_DIR}/istio/install.sh"
source "${NETWORKING_DIR}/istio/verify.sh"



install_networking() {

    log_info "Installing platform networking..."

    echo

    install_service_mesh

    echo

    log_success "Platform networking installed."

}

install_service_mesh() {

    log_info "Installing service mesh..."

    echo

    install_istio

    echo

    log_success "Service mesh installed."

}


main() {

    install_networking

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main
fi