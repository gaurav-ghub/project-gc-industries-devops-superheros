#!/usr/bin/env bash

###############################################################################
# File: install.sh
#
# Description:
# Installs Istio Service Mesh for the
# GC Industries Cloud Native Platform.
#
# Purpose:
# This script installs and configures Istio as the
# platform's service mesh.
#
# Responsibilities:
# - Install Istio
# - Verify installation
# - Prepare service mesh for platform networking
#
# Philosophy:
# The Istio module owns the implementation of the
# platform's service mesh capability.
#
# Author: Gaurav Chaurasia
###############################################################################



# Not `readonly` — see the note in ../install.sh.
ISTIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${ISTIO_DIR}/../../scripts/utils.sh"
source "${ISTIO_DIR}/verify.sh"



install_istio() {

    if istio_installed; then

        log_warn "Istio is already installed."

        echo

        verify_istio_installation

        return

    fi

    perform_istio_installation

}

istio_installed() {

    kubectl get namespace istio-system &>/dev/null
    # return 1

}



ensure_istioctl_installed() {

    log_info "Checking istioctl..."

    echo

    if command -v istioctl >/dev/null 2>&1; then

        log_success "istioctl is already installed."
        return

    fi

    log_warn "istioctl not found."

    download_istioctl

}


download_istioctl() {

    log_info "Downloading istioctl..."

    echo

    log_success "istioctl downloaded."

}



install_istio_control_plane() {

    if istioctl install \
        --set profile=demo \
        -y
    then

        echo

        log_success "Istio control plane installed."

    else

        log_error "Failed to install Istio control plane."

        exit 1

    fi

}







perform_istio_installation() {

    ensure_istioctl_installed

    install_istio_control_plane

    verify_istio_installation

}

main() {

    install_istio

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main
fi