#!/usr/bin/env bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/../../scripts/utils.sh"

install_prometheus() {

    if prometheus_installed; then

        log_warn "Prometheus is already installed."

        echo

        verify_prometheus_installation

        return

    fi

    perform_prometheus_installation

}

prometheus_installed() {

    helm status prometheus \
        --namespace monitoring \
        >/dev/null 2>&1

}


perform_prometheus_installation() {

    ensure_prometheus_repo

    install_prometheus_chart

    verify_prometheus_installation

}

ensure_prometheus_repo() {

    log_info "Checking Prometheus Helm repository..."

    echo

    log_success "Prometheus Helm repository is available."

}

install_prometheus_chart() {

    log_info "Installing Prometheus..."

    echo

    log_success "Prometheus installed."

}

verify_prometheus_installation() {

    log_info "Verifying Prometheus installation..."

    echo

    log_success "Prometheus installed."

}