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



# declared_istio_version reads platform/networking/versions.yaml.
#
# Deliberately sed and not yq: this is the first thing bootstrap does, and
# requiring a YAML processor to find out which Istio to install would make the
# platform's own prerequisites depend on a tool the prerequisite check has not
# run yet.
declared_istio_version() {

    sed -n '/^istio:/,/^[^[:space:]#]/p' "${ISTIO_DIR}/../versions.yaml" \
        | sed -n 's/^[[:space:]]*version:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}[[:space:]]*$/\1/p' \
        | head -1

}


# istioctl prints either "1.23.1" or "client version: 1.23.1" depending on the
# release, so take the last field rather than the whole line.
installed_istioctl_version() {

    istioctl version --remote=false --short 2>/dev/null \
        | head -1 \
        | awk '{print $NF}'

}


# The platform installs the Istio it says it installs.
#
# istioctl ships the control plane it was built with, so this cannot select a
# version — it can only refuse to install one the platform was never verified
# against. Before Phase 5 versions.yaml claimed 1.28.0 and the cluster ran
# 1.23.1 for months, because nothing ever compared the two.
verify_istioctl_version() {

    local declared installed

    declared="$(declared_istio_version)"
    installed="$(installed_istioctl_version)"

    if [[ -z "${declared}" ]]; then

        log_error "Could not read istio.version from platform/networking/versions.yaml."

        exit 1

    fi

    if [[ -z "${installed}" ]]; then

        log_error "Could not determine the istioctl version."

        exit 1

    fi

    if [[ "${declared}" != "${installed}" ]]; then

        log_error "Istio version mismatch."

        echo

        echo "  platform/networking/versions.yaml declares : ${declared}"
        echo "  istioctl on PATH is                        : ${installed}"

        echo

        echo "istioctl installs the control plane bundled with itself, so the platform"
        echo "cannot install ${declared} using an istioctl ${installed}. Either:"

        echo

        echo "  • install istioctl ${declared}:"
        echo "      curl -L https://istio.io/downloadIstio | ISTIO_VERSION=${declared} sh -"

        echo

        echo "  • or, if ${installed} is what you actually want, set it in"
        echo "    platform/networking/versions.yaml and re-verify the mesh features"
        echo "    that depend on it (canary VirtualService/DestinationRule need >= 1.22)."

        echo

        exit 1

    fi

    log_success "istioctl ${installed} matches the declared platform version."

    echo

}


ensure_istioctl_installed() {

    log_info "Checking istioctl..."

    echo

    if command -v istioctl >/dev/null 2>&1; then

        log_success "istioctl is already installed."

        echo

        verify_istioctl_version

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