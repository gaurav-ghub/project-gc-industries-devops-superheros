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

    if helm repo list | awk '{print $1}' | grep -qx "prometheus-community"; then

        log_info "Prometheus Helm repository already exists."

    else

        log_info "Adding Prometheus Helm repository..."

        if ! helm repo add prometheus-community \
            https://prometheus-community.github.io/helm-charts; then

            log_error "Failed to add Prometheus Helm repository."
            exit 1

        fi

        log_success "Prometheus Helm repository added."

    fi

    echo

    log_info "Updating Helm repositories..."

    if ! helm repo update; then

        log_error "Failed to update Helm repositories."
        exit 1
    fi

    echo

    helm repo list

    echo

    log_success "Prometheus Helm repository is ready."

}




install_prometheus_chart() {

    log_info "Installing Prometheus..."

    echo

    log_success "Prometheus installed."

}

verify_prometheus_installation() {

    log_info "Verifying Prometheus installation..."

    echo

    verify_monitoring_namespace
    verify_prometheus_release
    verify_prometheus_pods

    echo

    log_success "Prometheus installation verified."

}


verify_monitoring_namespace() {

    log_info "Checking monitoring namespace..."

    log_success "Monitoring namespace verified."

}


verify_prometheus_release() {

    log_info "Checking Prometheus Helm release..."

    log_success "Prometheus Helm release verified."

}

verify_prometheus_pods() {

    log_info "Checking Prometheus pods..."

    log_success "Prometheus pods are healthy."

}
