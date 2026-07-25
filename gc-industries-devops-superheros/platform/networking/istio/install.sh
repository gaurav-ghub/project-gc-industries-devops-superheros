#!/usr/bin/env bash

###############################################################################
# File: install.sh
#
# Description:
# Installs Istio Service Mesh for the
# Endurance.
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

        # An Istio that predates the access layer has a LoadBalancer ingress
        # gateway with no address, so every URL Endurance prints would be
        # unreachable while the module reported a clean skip. Reconcile it.
        ensure_ingress_gateway_published

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



###############################################################################
# The ingress gateway
#
# The demo profile's istio-ingressgateway is a LoadBalancer Service. On kind
# nothing answers that, so it stays <pending> forever and the mesh has a front
# door nobody outside the cluster can open — which is why every dashboard used
# to be behind a `kubectl port-forward`. kind-gateway.yaml pins the Service to
# NodePort on the two numbers kind-config.yaml publishes to the host.
###############################################################################

GATEWAY_OVERLAY="${ISTIO_DIR}/kind-gateway.yaml"
INGRESS_GATEWAY_SERVICE="istio-ingressgateway"
INGRESS_GATEWAY_NAMESPACE="istio-system"


# declared_node_port reads a nodePort out of the overlay by its port name.
#
# awk rather than yq, for the same reason declared_istio_version uses sed: this
# runs before the platform has installed anything, and requiring a YAML
# processor to find out which port the front door listens on would make the
# platform's own prerequisites depend on a tool nothing has checked for.
#
# The block ends at the next `- name:` and not at the next `nodePort:`, so a
# port that declares none — status-port — reports nothing rather than borrowing
# the number belonging to the entry below it.
declared_node_port() {

    local name="$1"

    awk -v want="${name}" '
        $1 == "-" && $2 == "name:" { inblock = ($3 == want) ; next }
        inblock && $1 == "nodePort:" { print $2 ; exit }
    ' "${GATEWAY_OVERLAY}"

}


# current_node_port reads the nodePort the cluster is actually serving.
current_node_port() {

    local name="$1"

    kubectl get service "${INGRESS_GATEWAY_SERVICE}" \
        -n "${INGRESS_GATEWAY_NAMESPACE}" \
        -o "jsonpath={.spec.ports[?(@.name=='${name}')].nodePort}" \
        2>/dev/null

}


ingress_gateway_published() {

    local want_http want_https got_http got_https type

    want_http="$(declared_node_port http2)"
    want_https="$(declared_node_port https)"

    type="$(kubectl get service "${INGRESS_GATEWAY_SERVICE}" \
        -n "${INGRESS_GATEWAY_NAMESPACE}" \
        -o jsonpath='{.spec.type}' 2>/dev/null)"

    got_http="$(current_node_port http2)"
    got_https="$(current_node_port https)"

    [[ "${type}" == "NodePort" ]] \
        && [[ -n "${want_http}" && "${got_http}" == "${want_http}" ]] \
        && [[ -n "${want_https}" && "${got_https}" == "${want_https}" ]]

}


verify_ingress_gateway_published() {

    log_info "Verifying the ingress gateway is published..."

    if ingress_gateway_published; then

        log_success "Ingress gateway is a NodePort on $(declared_node_port http2)/$(declared_node_port https)."

        echo

        return 0

    fi

    log_error "The ingress gateway is not published on the platform's node ports."

    echo

    kubectl get service "${INGRESS_GATEWAY_SERVICE}" -n "${INGRESS_GATEWAY_NAMESPACE}" 2>&1 || true

    echo

    echo "Endurance publishes the mesh's front door on the node ports declared in"
    echo "platform/networking/istio/kind-gateway.yaml, which kind-config.yaml maps to"
    echo 'the host. Without them every address `endurance urls` prints is unreachable.'

    echo

    return 1

}


# ensure_ingress_gateway_published converts a gateway installed before the
# access layer existed. `istioctl install` is an upgrade in place, so re-running
# it with the overlay is the whole fix; it is not run unconditionally because on
# a correct cluster it costs a minute and changes nothing.
ensure_ingress_gateway_published() {

    if ingress_gateway_published; then

        log_info "Ingress gateway is already published on the platform's node ports."

        echo

        return 0

    fi

    log_info "Ingress gateway is not published on the platform's node ports — reconciling."

    echo

    ensure_istioctl_installed

    install_istio_control_plane

    if ! verify_ingress_gateway_published; then

        log_error "Reconciling the ingress gateway did not publish it."

        exit 1

    fi

}


install_istio_control_plane() {

    if istioctl install \
        -f "${GATEWAY_OVERLAY}" \
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

    if ! verify_ingress_gateway_published; then

        exit 1

    fi

}

main() {

    install_istio

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main
fi